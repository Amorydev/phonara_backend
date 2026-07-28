package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phonara/backend/internal/integration/storage"
	"github.com/phonara/backend/internal/integration/tts"
)

// TTSBatchPayload giới hạn phạm vi một lần chạy.
type TTSBatchPayload struct {
	// Limit số câu xử lý mỗi lần. 0 = mặc định.
	Limit int `json:"limit"`
	// Force sinh lại cả những câu đã có URL (dùng khi đổi giọng).
	Force bool `json:"force"`
}

// NewTTSBatchTask tạo task sinh audio mẫu.
func NewTTSBatchTask(limit int, force bool) (*asynq.Task, error) {
	payload, err := json.Marshal(TTSBatchPayload{Limit: limit, Force: force})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return asynq.NewTask(TypeTTSBatch, payload, asynq.Queue("low")), nil
}

// handleTTSBatch sinh audio mẫu cho các câu assessment còn thiếu.
//
// Chạy MỘT LẦN cho mỗi câu rồi cache vĩnh viễn — nội dung câu là tĩnh, gọi TTS mỗi lần
// người dùng bấm "Nghe mẫu" là đốt tiền cho cùng một kết quả.
//
// Không có provider → để URL RỖNG, không bịa. URL chết tệ hơn URL trống: client không
// phân biệt được nếu không tốn một vòng mạng ~3,6 giây rồi mới biết là hỏng.
func handleTTSBatch(
	db *pgxpool.Pool, store storage.Store, provider tts.Provider, enqueuer *asynq.Client,
) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p TTSBatchPayload
		if len(t.Payload()) > 0 {
			if err := json.Unmarshal(t.Payload(), &p); err != nil {
				return fmt.Errorf("unmarshal payload: %w: %w", err, asynq.SkipRetry)
			}
		}
		if p.Limit <= 0 {
			p.Limit = defaultTTSBatchLimit
		}

		if _, err := provider.Synthesize(ctx, "", ""); errors.Is(err, tts.ErrNoProvider) {
			slog.Warn("bỏ qua sinh audio mẫu: chưa cấu hình TTS",
				"gợi_ý", "đặt AZURE_TTS_KEY và AZURE_TTS_REGION")
			return nil // không phải lỗi — chỉ là chưa bật tính năng
		}

		rows, err := db.Query(ctx,
			`SELECT q.id, q.text, COALESCE(s.locale, 'en-US')
			   FROM assessment_questions q
			   JOIN assessment_sets s ON s.id = q.set_id
			  WHERE q.is_active = TRUE
			    AND ($1 OR q.sample_audio_url IS NULL OR q.sample_audio_url = '')
			  ORDER BY q.order_index
			  LIMIT $2`,
			p.Force, p.Limit)
		if err != nil {
			return fmt.Errorf("truy vấn câu thiếu audio: %w", err)
		}

		type item struct{ id, text, locale string }
		var pending []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.text, &it.locale); err != nil {
				rows.Close()
				return fmt.Errorf("scan câu: %w", err)
			}
			pending = append(pending, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("duyệt câu: %w", err)
		}

		// content_items có HAI cột audio: audio_url_us và audio_url_uk. Đây không phải
		// dư thừa — users.target_accent cho người học chọn giọng Mỹ hay Anh, và phát
		// giọng Mỹ cho người đang luyện giọng Anh là dạy sai trọng âm lẫn nguyên âm.
		//
		// Mỗi accent là một bản ghi riêng trong danh sách chờ, sinh bằng giọng tương ứng.
		contentRows, err := db.Query(ctx,
			`SELECT id, text,
			        ($1 OR audio_url_us IS NULL OR audio_url_us = '') AS can_us,
			        ($1 OR audio_url_uk IS NULL OR audio_url_uk = '') AS can_uk
			   FROM content_items
			  WHERE is_active = TRUE
			    AND ($1
			         OR audio_url_us IS NULL OR audio_url_us = ''
			         OR audio_url_uk IS NULL OR audio_url_uk = '')
			  ORDER BY created_at
			  LIMIT $2`,
			p.Force, p.Limit)
		if err != nil {
			return fmt.Errorf("truy vấn content thiếu audio: %w", err)
		}
		var contentPending []contentItem
		for contentRows.Next() {
			var it contentItem
			if err := contentRows.Scan(&it.id, &it.text, &it.needUS, &it.needUK); err != nil {
				contentRows.Close()
				return fmt.Errorf("scan content: %w", err)
			}
			contentPending = append(contentPending, it)
		}
		contentRows.Close()
		if err := contentRows.Err(); err != nil {
			return fmt.Errorf("duyệt content: %w", err)
		}

		slog.Info("sinh audio mẫu",
			"assessment", len(pending), "content", len(contentPending),
			"provider", provider.Name())

		var done, failed int
		for i, it := range pending {
			// Free tier F0 siết rate chặt hơn S0 nhiều. Bắn 2.000 request liên tiếp sẽ
			// dính 429 hàng loạt, và mỗi lần dính là một câu phải chờ lô sau.
			if i > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(perRequestDelay):
				}
			}
			audio, err := provider.Synthesize(ctx, it.text, it.locale)
			if err != nil {
				// Một câu hỏng không được làm chết cả lô — lần chạy sau sẽ thử lại đúng
				// những câu còn thiếu, vì truy vấn lọc theo URL rỗng.
				slog.Error("tổng hợp thất bại", "question_id", it.id, "err", err)
				failed++
				continue
			}

			key := fmt.Sprintf("sample/assessment/%s.%s", it.id, audio.Extension)
			ref, err := store.Put(ctx, key, audio.Data)
			if err != nil {
				slog.Error("lưu audio thất bại", "question_id", it.id, "err", err)
				failed++
				continue
			}

			if _, err := db.Exec(ctx,
				`UPDATE assessment_questions SET sample_audio_url = $1 WHERE id = $2`,
				mediaURL(ref), it.id,
			); err != nil {
				slog.Error("cập nhật URL thất bại", "question_id", it.id, "err", err)
				failed++
				continue
			}
			done++
		}

		for _, it := range contentPending {
			for _, variant := range it.variants() {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(perRequestDelay):
				}

				audio, err := provider.SynthesizeVoice(ctx, it.text, variant.locale, variant.voice)
				if err != nil {
					slog.Error("tổng hợp content thất bại",
						"content_id", it.id, "accent", variant.accent, "err", err)
					failed++
					continue
				}
				key := fmt.Sprintf("sample/content/%s-%s.%s", it.id, variant.accent, audio.Extension)
				ref, err := store.Put(ctx, key, audio.Data)
				if err != nil {
					slog.Error("lưu audio content thất bại", "content_id", it.id, "err", err)
					failed++
					continue
				}
				// Tên cột ghép trong Go chứ không tham số hoá: Postgres không cho tham số
				// hoá tên cột, và `variant.column` là hằng nội bộ chứ không đến từ input
				// người dùng nên không có đường tiêm SQL.
				query := fmt.Sprintf(
					`UPDATE content_items SET %s = $1 WHERE id = $2`, variant.column)
				if _, err := db.Exec(ctx, query, mediaURL(ref), it.id); err != nil {
					slog.Error("cập nhật URL content thất bại",
						"content_id", it.id, "accent", variant.accent, "err", err)
					failed++
					continue
				}
				done++
			}
		}

		// ── Cặp âm dễ nhầm ──────────────────────────────────────────────────────
		//
		// Bắt buộc phải có audio: drill "nghe rồi chọn" không tồn tại được nếu không phát
		// được từ nào. Hai từ × hai accent = bốn cột, cùng khuôn variant với content_items.
		pairRows, err := db.Query(ctx,
			`SELECT id, word_a, word_b,
			        ($1 OR audio_a_us IS NULL OR audio_a_us = '') AS need_a_us,
			        ($1 OR audio_a_uk IS NULL OR audio_a_uk = '') AS need_a_uk,
			        ($1 OR audio_b_us IS NULL OR audio_b_us = '') AS need_b_us,
			        ($1 OR audio_b_uk IS NULL OR audio_b_uk = '') AS need_b_uk
			   FROM minimal_pairs
			  WHERE is_active = TRUE
			    AND ($1
			         OR audio_a_us IS NULL OR audio_a_us = ''
			         OR audio_a_uk IS NULL OR audio_a_uk = ''
			         OR audio_b_us IS NULL OR audio_b_us = ''
			         OR audio_b_uk IS NULL OR audio_b_uk = '')
			  ORDER BY difficulty, created_at
			  LIMIT $2`,
			p.Force, p.Limit)
		if err != nil {
			return fmt.Errorf("truy vấn cặp âm thiếu audio: %w", err)
		}
		var pairPending []minimalPairItem
		for pairRows.Next() {
			var it minimalPairItem
			if err := pairRows.Scan(&it.id, &it.wordA, &it.wordB,
				&it.needAUS, &it.needAUK, &it.needBUS, &it.needBUK); err != nil {
				pairRows.Close()
				return fmt.Errorf("scan cặp âm: %w", err)
			}
			pairPending = append(pairPending, it)
		}
		pairRows.Close()
		if err := pairRows.Err(); err != nil {
			return fmt.Errorf("duyệt cặp âm: %w", err)
		}

		for _, it := range pairPending {
			for _, variant := range it.variants() {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(perRequestDelay):
				}

				audio, err := provider.SynthesizeVoice(ctx, variant.word, variant.locale, variant.voice)
				if err != nil {
					slog.Error("tổng hợp cặp âm thất bại",
						"pair_id", it.id, "word", variant.word, "err", err)
					failed++
					continue
				}
				key := fmt.Sprintf("sample/minimal-pair/%s-%s.%s",
					it.id, variant.suffix, audio.Extension)
				ref, err := store.Put(ctx, key, audio.Data)
				if err != nil {
					slog.Error("lưu audio cặp âm thất bại", "pair_id", it.id, "err", err)
					failed++
					continue
				}
				// Tên cột là hằng nội bộ, không đến từ input người dùng — xem ghi chú ở
				// vòng lặp content_items phía trên.
				query := fmt.Sprintf(
					`UPDATE minimal_pairs SET %s = $1 WHERE id = $2`, variant.column)
				if _, err := db.Exec(ctx, query, mediaURL(ref), it.id); err != nil {
					slog.Error("cập nhật URL cặp âm thất bại", "pair_id", it.id, "err", err)
					failed++
					continue
				}
				done++
			}
		}

		// Đếm theo SỐ FILE chứ không theo số bản ghi: mỗi câu đánh giá cần 1 file, mỗi
		// content item cần tới 2 (US/UK), mỗi cặp âm cần tới 4 (2 từ × 2 accent). Trộn
		// hai đơn vị làm con số "còn lại" ra ÂM — đã thấy -102 trên lô thật.
		expected := len(pending)
		for _, it := range contentPending {
			expected += len(it.variants())
		}
		for _, it := range pairPending {
			expected += len(it.variants())
		}
		slog.Info("sinh audio mẫu xong",
			"thành_công", done, "thất_bại", failed,
			"tổng_file_cần", expected,
			"còn_lại", max(expected-done-failed, 0))

		// Còn việc thì tự đẩy lô tiếp. Ở quy mô 2.000 câu, bắt người vận hành chạy tay
		// 20 lần là thiết kế tồi — và dễ bỏ sót lô cuối mà không ai biết.
		//
		// Điều kiện dừng: lô này lấp đầy limit, tức có thể còn nữa. Lô không đầy nghĩa là
		// đã hết. Truy vấn lọc theo URL rỗng nên không có nguy cơ lặp vô hạn: mỗi lô
		// thành công đều thu hẹp tập còn lại.
		if done > 0 && (len(pending) >= p.Limit ||
			len(contentPending) >= p.Limit || len(pairPending) >= p.Limit) {
			next, err := NewTTSBatchTask(p.Limit, false)
			if err != nil {
				return fmt.Errorf("tạo lô tiếp: %w", err)
			}
			if _, err := enqueuer.EnqueueContext(ctx, next,
				asynq.ProcessIn(batchCooldown),
			); err != nil {
				return fmt.Errorf("đẩy lô tiếp: %w", err)
			}
			slog.Info("đã đẩy lô tiếp", "sau", batchCooldown)
		}
		return nil
	}
}

// mediaURL đổi audio_ref nội bộ thành đường dẫn client gọi được.
//
// Phải bỏ tiền tố scheme của storage ("file://"): nó là chi tiết cài đặt phía server, để
// lọt vào URL sẽ tạo ra "/v1/media/file://sample/..." — không parse được ở client.
//
// Trả đường dẫn TƯƠNG ĐỐI chứ không tuyệt đối: host đổi theo môi trường (localhost khi
// dev, domain thật khi production), ghi cứng host vào CSDL sẽ khiến dữ liệu dev không
// dùng được ở production và ngược lại. Handler API nối host vào khi trả về cho client.
func mediaURL(audioRef string) string {
	return "/v1/media/" + strings.TrimPrefix(audioRef, "file://")
}

// contentItem là một câu nội dung cùng với các accent còn thiếu audio.
type contentItem struct {
	id     string
	text   string
	needUS bool
	needUK bool
}

type audioVariant struct {
	accent string
	locale string
	voice  string
	column string
}

// variants trả các accent cần sinh cho câu này.
func (c contentItem) variants() []audioVariant {
	var out []audioVariant
	if c.needUS {
		out = append(out, audioVariant{"us", "en-US", VoiceUS, "audio_url_us"})
	}
	if c.needUK {
		out = append(out, audioVariant{"uk", "en-GB", VoiceUK, "audio_url_uk"})
	}
	return out
}

// minimalPairItem là một cặp âm cùng với các bản audio còn thiếu.
type minimalPairItem struct {
	id           string
	wordA, wordB string
	needAUS      bool
	needAUK      bool
	needBUS      bool
	needBUK      bool
}

// pairVariant là một bản audio cần sinh: một TỪ ở một accent.
type pairVariant struct {
	word   string
	locale string
	voice  string
	column string
	suffix string
}

// variants trả các bản audio còn thiếu của cặp này.
//
// Bốn cột vì có hai từ × hai accent — `users.target_accent` cho người học chọn giọng Mỹ
// hay Anh, và một cặp âm phát hai giọng khác nhau cho hai từ thì phép so sánh mất ý nghĩa:
// người học sẽ nghe ra khác biệt giữa hai GIỌNG chứ không phải giữa hai ÂM.
func (m minimalPairItem) variants() []pairVariant {
	var out []pairVariant
	if m.needAUS {
		out = append(out, pairVariant{m.wordA, "en-US", VoiceUS, "audio_a_us", "a-us"})
	}
	if m.needAUK {
		out = append(out, pairVariant{m.wordA, "en-GB", VoiceUK, "audio_a_uk", "a-uk"})
	}
	if m.needBUS {
		out = append(out, pairVariant{m.wordB, "en-US", VoiceUS, "audio_b_us", "b-us"})
	}
	if m.needBUK {
		out = append(out, pairVariant{m.wordB, "en-GB", VoiceUK, "audio_b_uk", "b-uk"})
	}
	return out
}

const (
	// Giọng mặc định cho hai accent. Người học chọn qua users.target_accent.
	VoiceUS = "en-US-AriaNeural"
	VoiceUK = "en-GB-SoniaNeural"

	defaultTTSBatchLimit = 100

	// Nghỉ giữa hai lần gọi TTS. Đủ thưa để không đụng rate limit free tier, đủ dày để
	// 2.000 câu xong trong khoảng một tiếng.
	perRequestDelay = 1500 * time.Millisecond

	// Nghỉ giữa hai lô, cho hạn mức hồi lại.
	batchCooldown = 30 * time.Second
)

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phonara/backend/internal/config"
	storedb "github.com/phonara/backend/internal/store/db"
	"github.com/phonara/backend/internal/worker"
)

func main() {
	// -tts: chỉ đẩy task sinh audio mẫu, không seed lại dữ liệu.
	ttsOnly := flag.Bool("tts", false, "đẩy task sinh audio mẫu rồi thoát")
	force := flag.Bool("force", false, "sinh lại cả câu đã có audio (dùng khi đổi giọng)")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	if *ttsOnly {
		if err := enqueueTTS(cfg, *force); err != nil {
			slog.Error("đẩy task tts", "err", err)
			os.Exit(1)
		}
		slog.Info("đã đẩy task sinh audio mẫu — xem log worker để theo dõi")
		return
	}

	ctx := context.Background()

	pool, err := storedb.NewPool(ctx, cfg.DB)
	if err != nil {
		slog.Error("connect db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	slog.Info("running seed...")

	if err := seedPreAssessment(ctx, pool); err != nil {
		slog.Error("seed pre-assessment", "err", err)
		os.Exit(1)
	}

	if err := seedDailyChallenges(ctx, pool); err != nil {
		slog.Error("seed daily challenges", "err", err)
		os.Exit(1)
	}

	if err := seedPracticeModes(ctx, pool); err != nil {
		slog.Error("seed practice modes", "err", err)
		os.Exit(1)
	}

	// Thứ tự bắt buộc: fix_guides và minimal_pairs đều trỏ tới l1_error_tags qua khoá
	// ngoại, và cả hai runner đều BÁO LỖI nếu nhãn chưa tồn tại thay vì để liên kết rỗng.
	if err := seedL1ErrorTags(ctx, pool); err != nil {
		slog.Error("seed l1 error tags", "err", err)
		os.Exit(1)
	}

	if err := seedFixGuides(ctx, pool); err != nil {
		slog.Error("seed fix guides", "err", err)
		os.Exit(1)
	}

	if err := seedMinimalPairs(ctx, pool); err != nil {
		slog.Error("seed minimal pairs", "err", err)
		os.Exit(1)
	}

	if err := seedBadges(ctx, pool); err != nil {
		slog.Error("seed badges", "err", err)
		os.Exit(1)
	}

	if err := seedLegalDocuments(ctx, pool); err != nil {
		slog.Error("seed legal documents", "err", err)
		os.Exit(1)
	}

	if err := seedPlanConfigs(ctx, pool); err != nil {
		slog.Error("seed plan configs", "err", err)
		os.Exit(1)
	}

	slog.Info("seed complete")
}

// seedNS namespaces deterministic seed UUIDs so re-running upserts the same rows.
var seedNS = uuid.NewSHA1(uuid.NameSpaceURL, []byte("phonara.seed"))

// seedID derives a stable UUID for a given seed key.
func seedID(name string) uuid.UUID { return uuid.NewSHA1(seedNS, []byte(name)) }

// assessmentQuestion is a row of seed data for the pre-assessment set.
type assessmentQuestion struct {
	order            int
	text             string
	phonetic         string
	sampleAudioURL   string
	expectedDuration int
	difficulty       int
}

// ptrInt returns a pointer for an optional int, treating 0 as "not set".
func ptrInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

// seedPreAssessment inserts the default onboarding pre-assessment set and its
// questions. It is idempotent: re-running upserts the set (keyed by code+version)
// and each question (keyed by set_id+order_index).
func seedPreAssessment(ctx context.Context, pool *pgxpool.Pool) error {
	const (
		setCode    = "pre_assessment_default"
		benchCode  = "benchmark_full"
		setVersion = 1
		// KHÔNG bịa URL audio. Trước đây seed gán sẵn cdn.phonara.app/qN.mp3 cho 7 câu
		// đầu, nhưng CDN đó không tồn tại (trả 404) — client bấm "Nghe mẫu" phải chờ ~3,6
		// giây một vòng mạng rồi mới biết là hỏng. URL chết tệ hơn URL trống.
		//
		// Audio mẫu do worker sinh: asynq task tts:batch quét câu có sample_audio_url rỗng,
		// gọi Azure Neural TTS, lưu vào storage rồi ghi URL thật vào đây.
	)

	var setID uuid.UUID
	err := pool.QueryRow(ctx,
		// `is_default = TRUE` phải khai TƯỜNG MINH ở đây.
		//
		// Migration 000009 có câu `UPDATE ... SET is_default = TRUE` nhưng nó chỉ chạm vào
		// dòng ĐÃ TỒN TẠI. Trên database mới tinh, migration chạy TRƯỚC seed nên không có
		// gì để update, và cột mặc định FALSE — kết quả là không bộ nào là mặc định,
		// `GetPreAssessment` trả 404 và onboarding chết ngay từ màn hình đầu.
		//
		// Lỗi này KHÔNG lộ ra trên máy dev vì ở đó dữ liệu có trước migration. Nó chỉ xuất
		// hiện đúng lúc deploy lên máy sạch — và đã xảy ra thật.
		`INSERT INTO assessment_sets (code, type, title, description, locale, version, is_default)
		 VALUES ($1, 'pre_assessment', $2, $3, 'en-US', $4, TRUE)
		 ON CONFLICT (code, version) DO UPDATE
		   SET title = EXCLUDED.title,
		       description = EXCLUDED.description,
		       is_default = TRUE,
		       is_active = TRUE
		 RETURNING id`,
		setCode,
		"Pre-Assessment",
		"Đánh giá trình độ phát âm ban đầu trong onboarding.",
		setVersion,
	).Scan(&setID)
	if err != nil {
		return err
	}

	// Thứ tự câu trong bộ ONBOARDING. Chọn bằng phân tích phủ âm chứ không cảm tính:
	// 4 câu {7,13,18,23} đủ cho mỗi âm khó (θ ð ʃ ʒ tʃ dʒ v z) xuất hiện ≥2 lần và phủ
	// cả 13 âm mục tiêu. Thêm câu 1 và 2 ở đầu để có độ dốc 1→5 — mở màn bằng
	// "thirty-three thirsty travelers thrived" sẽ làm người mới nản ngay câu đầu.
	onboardingOrders := []int{1, 2, 13, 18, 23, 7}

	questions := []assessmentQuestion{
		{1, "The weather is nice today.", "/ðə ˈwɛðər ɪz naɪs təˈdeɪ/", "", 4, 1},
		{2, "I think this is great.", "/aɪ θɪŋk ðɪs ɪz ɡreɪt/", "", 4, 2},
		{3, "She sells seashells by the seashore.", "/ʃi sɛlz ˈsiˌʃɛlz baɪ ðə ˈsiˌʃɔr/", "", 6, 3},
		{4, "Could you please repeat that question?", "/kʊd ju pliz rɪˈpit ðæt ˈkwɛstʃən/", "", 5, 2},
		{5, "Our team launched the product last month.", "/aʊər tim lɔntʃt ðə ˈprɑdəkt læst mʌnθ/", "", 6, 3},
		{6, "Thank you very much for your help.", "/θæŋk ju ˈvɛri mʌtʃ fɔr jʊr hɛlp/", "", 4, 2},
		{7, "The thirty-three thirsty travelers thrived.", "/ðə ˈθɜrti θri ˈθɜrsti ˈtrævələrz θraɪvd/", "", 7, 5},
		{8, "We really enjoy learning new languages.", "/wi ˈrɪəli ɪnˈdʒɔɪ ˈlɜrnɪŋ nu ˈlæŋɡwɪdʒɪz/", "", 5, 2},
		{9, "Please leave the blue glass on the table.", "/pliz liv ðə blu ɡlæs ɑn ðə ˈteɪbəl/", "", 5, 2},
		{10, "Victor wore a very warm vest.", "/ˈvɪktər wɔr ə ˈvɛri wɔrm vɛst/", "", 5, 3},
		{11, "The rice is on the right shelf.", "/ðə raɪs ɪz ɑn ðə raɪt ʃɛlf/", "", 4, 2},
		{12, "I packed my bag and walked to class.", "/aɪ pækt maɪ bæɡ ænd wɔkt tə klæs/", "", 5, 3},
		{13, "George chose an orange jacket.", "/dʒɔrdʒ tʃoʊz ən ˈɔrɪndʒ ˈdʒækɪt/", "", 5, 3},
		{14, "This ship will leave in fifteen minutes.", "/ðɪs ʃɪp wɪl liv ɪn ˌfɪfˈtin ˈmɪnɪts/", "", 5, 3},
		{15, "Put the full spoon near the blue bowl.", "/pʊt ðə fʊl spun nɪr ðə blu boʊl/", "", 5, 3},
		{16, "The black cat jumped over the cup.", "/ðə blæk kæt dʒʌmpt ˈoʊvər ðə kʌp/", "", 5, 3},
		{17, "Three friends finished their work early.", "/θri frɛndz ˈfɪnɪʃt ðɛr wɜrk ˈɜrli/", "", 6, 4},
		{18, "She watched six short videos yesterday.", "/ʃi wɑtʃt sɪks ʃɔrt ˈvɪdioʊz ˈjɛstərdeɪ/", "", 6, 4},
		{19, "Would you like to order some fresh fruit?", "/wʊd ju laɪk tə ˈɔrdər sʌm frɛʃ frut/", "", 6, 4},
		{20, "The world needs better communication.", "/ðə wɜrld nidz ˈbɛtər kəˌmjunəˈkeɪʃən/", "", 6, 4},

		// Ba câu dưới thêm để phủ âm /ʒ/ — r1/inventory.py phát hiện 20 câu trên KHÔNG
		// có câu nào chứa nó. Thêm người đọc không cứu được: 0 × N người vẫn là 0, nên
		// tiêu chí "không lớp âm nào sai hệ thống" (§6.4) sẽ không đánh giá được /ʒ/.
		//
		// Mỗi câu chứa 2 lần /ʒ/ → 6 lần mỗi lượt đọc, tức 120 lần với 20 người, vượt
		// sàn ≥30 của §6.1. Câu đã kiểm bằng chính tokenizer của model, không phỏng đoán.
		{21, "Usually I measure my own progress.", "/ˈjuʒuəli aɪ ˈmɛʒər maɪ oʊn ˈprɑɡrɛs/", "", 6, 4},
		{22, "The decision was a pleasure to make.", "/ðə dɪˈsɪʒən wʌz ə ˈplɛʒər tə meɪk/", "", 6, 4},
		{23, "She has a clear vision for the garage.", "/ʃi hæz ə klɪr ˈvɪʒən fɔr ðə ɡəˈrɑʒ/", "", 6, 4},
	}

	// Bộ onboarding chỉ nhận ĐÚNG các câu được chọn, đánh số 1..N ngay từ đầu.
	//
	// Trước đây seed chèn cả 23 câu rồi xoá 17 câu thừa rồi đánh số lại. Cách đó làm hỏng
	// audio: mỗi lần chạy lại, câu ở vị trí 3..6 bị xoá rồi chèn lại với UUID MỚI, mà tên
	// file audio đặt theo UUID — nên MP3 vẫn nằm trong MinIO nhưng CSDL mất con trỏ.
	//
	// Đo được sau một lần chạy lại: 2/6 câu còn audio. Hai câu sống sót đúng là hai câu có
	// vị trí đích trùng vị trí gốc (1→1, 2→2), tức cùng một dòng được cập nhật tại chỗ.
	//
	// Chèn thẳng danh sách đã chọn thì `order_index` ổn định qua mọi lần chạy, dòng không
	// bị luân chuyển, và audio giữ nguyên. Bỏ luôn được màn xoá + đánh số lại hai pha.
	byOrder := make(map[int]assessmentQuestion, len(questions))
	for _, q := range questions {
		byOrder[q.order] = q
	}
	onboarding := make([]assessmentQuestion, 0, len(onboardingOrders))
	for i, srcOrder := range onboardingOrders {
		q, ok := byOrder[srcOrder]
		if !ok {
			return fmt.Errorf("onboardingOrders trỏ tới câu %d không tồn tại", srcOrder)
		}
		q.order = i + 1 // vị trí trình bày, không phải vị trí trong danh sách gốc
		onboarding = append(onboarding, q)
	}

	for _, q := range onboarding {
		_, err := pool.Exec(ctx,
			`INSERT INTO assessment_questions
			   (set_id, order_index, text, phonetic, sample_audio_url, expected_duration, difficulty)
			 VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7)
			 ON CONFLICT (set_id, order_index) DO UPDATE
			   SET text = EXCLUDED.text,
			       phonetic = EXCLUDED.phonetic,
			       -- GIỮ audio đã sinh. Seed không biết gì về audio (nó luôn đưa vào
			       -- NULL), nên ghi đè bằng EXCLUDED sẽ xoá sạch URL do worker tts:batch
			       -- tạo — file vẫn nằm trong MinIO nhưng CSDL mất con trỏ, và lần chạy
			       -- seed sau lại tốn tiền sinh lại toàn bộ.
			       --
			       -- Chỉ nhận giá trị mới khi seed thực sự có URL (trường hợp import
			       -- audio thu sẵn từ nguồn ngoài).
			       sample_audio_url = COALESCE(EXCLUDED.sample_audio_url, assessment_questions.sample_audio_url),
			       expected_duration = EXCLUDED.expected_duration,
			       difficulty = EXCLUDED.difficulty,
			       is_active = TRUE`,
			setID, q.order, q.text, q.phonetic, q.sampleAudioURL,
			ptrInt(q.expectedDuration), ptrInt(q.difficulty),
		)
		if err != nil {
			return err
		}
	}

	slog.Info("seeded pre-assessment", "set_id", setID, "questions", len(onboarding))

	// ── bộ benchmark: TẤT CẢ câu ──────────────────────────────────────────────
	// Onboarding cần ngắn, benchmark cần phủ đủ. Một bộ không phục vụ được cả hai.
	var benchID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO assessment_sets (code, type, title, description, locale, version, is_default)
		 VALUES ($1, 'pre_assessment', $2, $3, 'en-US', $4, FALSE)
		 ON CONFLICT (code, version) DO UPDATE SET title = EXCLUDED.title
		 RETURNING id`,
		benchCode, "Benchmark (đầy đủ)",
		"Toàn bộ câu, dùng cho benchmark bước 7. KHÔNG phục vụ onboarding.",
		setVersion,
	).Scan(&benchID); err != nil {
		return fmt.Errorf("seed benchmark set: %w", err)
	}
	for _, q := range questions {
		if _, err := pool.Exec(ctx,
			`INSERT INTO assessment_questions
			   (set_id, order_index, text, phonetic, sample_audio_url, expected_duration, difficulty)
			 VALUES ($1, $2, $3, $4, NULL, $5, $6)
			 ON CONFLICT (set_id, order_index) DO UPDATE
			   SET text = EXCLUDED.text, phonetic = EXCLUDED.phonetic,
			       sample_audio_url = COALESCE(EXCLUDED.sample_audio_url, assessment_questions.sample_audio_url),
			       expected_duration = EXCLUDED.expected_duration,
			       difficulty = EXCLUDED.difficulty, is_active = TRUE`,
			benchID, q.order, q.text, q.phonetic,
			ptrInt(q.expectedDuration), ptrInt(q.difficulty),
		); err != nil {
			return fmt.Errorf("seed benchmark question %d: %w", q.order, err)
		}
	}
	slog.Info("seeded benchmark set", "set_id", benchID, "questions", len(questions))

	// Dọn câu thừa còn sót từ các lần seed CŨ (khi seed chèn cả 23 câu vào bộ onboarding
	// rồi mới xoá bớt). Với logic hiện tại thì bộ này chỉ bao giờ có đúng len(onboarding)
	// câu, nên câu lệnh này là dọn dẹp một lần, không phải phần của luồng bình thường.
	if _, err := pool.Exec(ctx,
		`DELETE FROM assessment_questions
		  WHERE set_id = $1 AND order_index > $2`,
		setID, len(onboarding),
	); err != nil {
		return fmt.Errorf("dọn câu thừa của bộ onboarding: %w", err)
	}

	slog.Info("bộ onboarding", "số_câu", len(onboarding))
	return nil
}

// seedContentItem is one word/sentence row for the content library.
type seedContentItem struct {
	key        string
	typ        string // word | sentence
	text       string
	ipa        string
	audioUS    string
	difficulty int
	focus      []string
}

// seedDailyChallenges seeds a small content library, one shadowing passage, and
// daily challenge bundles for yesterday/today/tomorrow (UTC) so the feature is
// demonstrable across user timezones. Idempotent via deterministic UUIDs.
func seedDailyChallenges(ctx context.Context, pool *pgxpool.Pool) error {
	// Không bịa URL — xem ghi chú ở seedPreAssessment. Worker tts:batch sẽ sinh và ghi
	// URL thật cho cả content_items lẫn assessment_questions.

	// 1. Content library (words + sentences) used as challenge items.
	content := []seedContentItem{
		{"word_comfortable", "word", "comfortable", "/ˈkʌmftəbəl/", "", 3, []string{"ə"}},
		{"sent_restaurant", "sentence", "Could you recommend a good restaurant?", "/kʊd ju ˌrɛkəˈmɛnd ə ɡʊd ˈrɛstrɒnt/", "", 2, []string{"r"}},
		{"sent_meeting", "sentence", "I'd like to schedule a meeting tomorrow.", "/aɪd laɪk tə ˈskɛdʒul ə ˈmitɪŋ təˈmɒroʊ/", "", 3, []string{"dʒ"}},
	}
	contentID := make(map[string]uuid.UUID, len(content))
	for _, c := range content {
		id := seedID("content:" + c.key)
		contentID[c.key] = id
		focus := c.focus
		if focus == nil {
			focus = []string{}
		}
		_, err := pool.Exec(ctx,
			`INSERT INTO content_items (id, type, text, ipa, audio_url_us, difficulty, focus_phonemes, is_active)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
			 ON CONFLICT (id) DO UPDATE
			   SET type = EXCLUDED.type, text = EXCLUDED.text, ipa = EXCLUDED.ipa,
			       audio_url_us = EXCLUDED.audio_url_us, difficulty = EXCLUDED.difficulty,
			       focus_phonemes = EXCLUDED.focus_phonemes, is_active = TRUE`,
			id, c.typ, c.text, c.ipa, c.audioUS, c.difficulty, focus)
		if err != nil {
			return err
		}
	}

	// 2. One shadowing passage + its sentences.
	passageID := seedID("passage:morning_routine")
	passageSentences := []struct {
		order int
		text  string
		ipa   string
		audio string
	}{
		{1, "Every morning I wake up at six.", "/ˈɛvri ˈmɔrnɪŋ aɪ weɪk ʌp æt sɪks/", ""},
		{2, "I make a cup of coffee and read the news.", "/aɪ meɪk ə kʌp əv ˈkɔfi ænd rid ðə nuz/", ""},
		{3, "Then I get ready and head to work.", "/ðɛn aɪ ɡɛt ˈrɛdi ænd hɛd tə wɜrk/", ""},
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO shadowing_passages (id, title, source, topic, difficulty, sentence_count, is_active)
		 VALUES ($1, $2, 'curated', $3, $4, $5, TRUE)
		 ON CONFLICT (id) DO UPDATE
		   SET title = EXCLUDED.title, topic = EXCLUDED.topic, difficulty = EXCLUDED.difficulty,
		       sentence_count = EXCLUDED.sentence_count, is_active = TRUE`,
		passageID, "Morning Routine", "daily_life", 2, len(passageSentences))
	if err != nil {
		return err
	}
	for _, s := range passageSentences {
		_, err := pool.Exec(ctx,
			`INSERT INTO passage_sentences (id, passage_id, order_index, text, ipa, native_audio_url)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (passage_id, order_index) DO UPDATE
			   SET text = EXCLUDED.text, ipa = EXCLUDED.ipa, native_audio_url = EXCLUDED.native_audio_url`,
			seedID(fmt.Sprintf("passage:morning_routine:s%d", s.order)), passageID, s.order, s.text, s.ipa, s.audio)
		if err != nil {
			return err
		}
	}

	// 3. Daily challenge bundles for D-1, D, D+1 (UTC) — covers every timezone.
	type item struct {
		kind       string
		contentKey string // set for word/sentence
		passage    bool   // true for the passage item
	}
	bundle := []item{
		{kind: "word", contentKey: "word_comfortable"},
		{kind: "sentence", contentKey: "sent_restaurant"},
		{kind: "sentence", contentKey: "sent_meeting"},
		{kind: "passage", passage: true},
	}

	for _, offset := range []int{-1, 0, 1} {
		date := time.Now().UTC().AddDate(0, 0, offset).Format("2006-01-02")
		challengeID := seedID("daily:" + date)
		_, err := pool.Exec(ctx,
			`INSERT INTO daily_challenges (id, date, title, description, category, banner_url, moderated)
			 VALUES ($1, $2, $3, $4, $5, $6, TRUE)
			 ON CONFLICT (id) DO UPDATE
			   SET date = EXCLUDED.date, title = EXCLUDED.title, description = EXCLUDED.description,
			       category = EXCLUDED.category, banner_url = EXCLUDED.banner_url, moderated = TRUE`,
			challengeID, date,
			"Thử thách phát âm hằng ngày",
			"Luyện một bộ từ, câu và đoạn ngắn để giữ streak.",
			"pronunciation",
			"https://cdn.phonara.app/daily/banner.png",
		)
		if err != nil {
			return err
		}

		for i, it := range bundle {
			order := i + 1
			var cID, pID *uuid.UUID
			if it.passage {
				p := passageID
				pID = &p
			} else {
				c := contentID[it.contentKey]
				cID = &c
			}
			_, err := pool.Exec(ctx,
				`INSERT INTO daily_challenge_items (id, challenge_id, order_index, kind, content_item_id, passage_id)
				 VALUES ($1, $2, $3, $4, $5, $6)
				 ON CONFLICT (challenge_id, order_index) DO UPDATE
				   SET kind = EXCLUDED.kind, content_item_id = EXCLUDED.content_item_id,
				       passage_id = EXCLUDED.passage_id`,
				seedID(fmt.Sprintf("daily:%s:item%d", date, order)), challengeID, order, it.kind, cID, pID)
			if err != nil {
				return err
			}
		}
		slog.Info("seeded daily challenge", "date", date, "items", len(bundle))
	}

	return nil
}

// seedPracticeModes seeds the 6 Home practice-mode cards. Idempotent on key.
func seedPracticeModes(ctx context.Context, pool *pgxpool.Pool) error {
	type mode struct {
		key, title, subtitle, icon, route string
		premium                           bool
	}
	modes := []mode{
		{"word", "Phát âm từ", "", "ic_word", "/practice/word", false},
		{"sentence", "Phát âm câu", "", "ic_sentence", "/practice/sentence", false},
		{"minimal_pair", "Cặp âm dễ nhầm", "", "ic_minimal_pair", "/minimal-pairs", false},
		{"shadowing", "Shadowing", "", "ic_shadowing", "/shadowing", false},
		{"flashcard", "Từ vựng", "Ôn tập Flashcard", "ic_flashcard", "/vocabulary", false},
		{"profile", "Hồ sơ phát âm", "", "ic_profile", "/coach/profile", false},
	}

	for i, m := range modes {
		_, err := pool.Exec(ctx,
			`INSERT INTO practice_modes (key, title_vi, subtitle_vi, icon, route, is_premium, order_index, is_active)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
			 ON CONFLICT (key) DO UPDATE
			   SET title_vi = EXCLUDED.title_vi, subtitle_vi = EXCLUDED.subtitle_vi,
			       icon = EXCLUDED.icon, route = EXCLUDED.route, is_premium = EXCLUDED.is_premium,
			       order_index = EXCLUDED.order_index, is_active = TRUE`,
			m.key, m.title, m.subtitle, m.icon, m.route, m.premium, i+1)
		if err != nil {
			return err
		}
	}

	slog.Info("seeded practice modes", "count", len(modes))
	return nil
}

// enqueueTTS đẩy task sinh audio mẫu vào hàng đợi.
//
// Đẩy task chứ không sinh trực tiếp ở đây: worker mới là nơi có provider TTS và storage,
// và chạy qua hàng đợi thì lặp lại được, theo dõi được, không phụ thuộc terminal còn mở.
func enqueueTTS(cfg *config.Config, force bool) error {
	client := asynq.NewClient(asynq.RedisClientOpt{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer client.Close()

	task, err := worker.NewTTSBatchTask(0, force)
	if err != nil {
		return err
	}
	_, err = client.Enqueue(task)
	return err
}

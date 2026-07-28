package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TypeErrorProfileRecompute là task type asynq cho việc tính lại hồ sơ lỗi.
//
// Nguồn sự thật nằm ở đây chứ không ở package worker: worker import service, nên đặt ngược
// lại sẽ tạo vòng import. `worker` lặp lại hằng số này và có test khẳng định hai giá trị
// khớp nhau — cùng khuôn với TypeAssessmentRun.
const TypeErrorProfileRecompute = "errorprofile:recompute"

// ErrorProfilePayload là payload task tính lại hồ sơ lỗi.
type ErrorProfilePayload struct {
	UserID string `json:"user_id"`
}

const (
	// Hoãn trước khi chạy, đủ để gom cả cụm kết quả của một phiên luyện vào một lần tính.
	errorProfileDebounce = 15 * time.Second
	// Trần thời gian một lần tính. Cửa sổ quan sát đã bị chặn nên đây là lưới an toàn cho
	// trường hợp DB chậm bất thường, không phải giới hạn thiết kế.
	errorProfileTimeout = 60 * time.Second
)

// Tham số EWMA và ngưỡng phân loại.
const (
	// ewmaAlpha — trọng số của quan sát MỚI NHẤT khi chuỗi đã đủ dài (xem `ewma`).
	//
	// 0,15 tương ứng cửa sổ hiệu dụng ≈ 1/α ≈ 7 lần đọc gần nhất.
	//
	// Chọn 0,15 chứ không phải 0,3 vì một ràng buộc sản phẩm cụ thể: **một lần đọc hỏng
	// không được làm tụt quá một bậc trạng thái.** Với α = 0,3, chuỗi năm lần 90 điểm rồi
	// một lần 0 điểm cho mastery 59,4 — nhảy thẳng từ `good` xuống `weak`, trong khi kỹ
	// năng thật gần như không đổi. Với 0,15 kết quả là 68,3, tức `improving`. Micro trục
	// trặc hay một lần ho không được xoá thành quả nhiều buổi luyện.
	ewmaAlpha = 0.15

	// Số quan sát gần nhất giữ lại cho MỖI phoneme.
	//
	// Đây là chặn chi phí, và nó KHÔNG làm sai kết quả: với α = 0,15, quan sát thứ 50 tính
	// từ mới nhất có trọng số 0,85^49 ≈ 3,5e-4 trên tổng trọng số 6,667 — đóng góp 0,0052%.
	// Cửa sổ này là hệ quả của chính EWMA chứ không phải một sự đánh đổi;
	// `TestEWMATruncationWindowIsNumericallyIrrelevant` giữ cho điều đó còn đúng nếu ai đó
	// chỉnh α.
	maxObservationsPerPhoneme = 50

	// Cửa sổ cho chỉ số mức item (accuracy, completeness toàn câu).
	maxItemObservations = 200

	// Ngưỡng trạng thái trên thang 0–100 (cùng thang `phoneme_scores.accuracy`).
	masteryWeakBelow   = 60.0
	masteryGoodAtLeast = 80.0

	topErrorsLimit = 5

	// Một lần đọc hỏng không đủ để gọi là "lỗi thường gặp". Ngưỡng này chặn việc đưa một
	// âm vào top_errors — và do đó vào Fix Guide — chỉ vì micro trục trặc một lần.
	minAttemptsForTopError = 3
)

// englishConsonants — dùng để nhận diện phụ âm cuối từ.
//
// Liệt kê phụ âm thay vì nguyên âm là cố ý: tập phụ âm tiếng Anh đóng và ổn định (24 âm),
// còn tập nguyên âm của espeak thì rộng và còn nhiều biến thể giảm (`ə`, `ɐ`, `ᵻ`, `ɚ`…).
// Liệt kê tập đóng thì âm lạ sẽ mặc định KHÔNG bị tính là phụ âm cuối — hướng sai an toàn
// hơn là bịa ra một chỉ số nuốt-phụ-âm từ ký hiệu ta chưa từng thấy.
var englishConsonants = map[string]bool{
	"p": true, "b": true, "t": true, "d": true, "k": true, "ɡ": true,
	"tʃ": true, "dʒ": true, "f": true, "v": true, "θ": true, "ð": true,
	"s": true, "z": true, "ʃ": true, "ʒ": true, "h": true,
	"m": true, "n": true, "ŋ": true, "l": true, "ɹ": true, "j": true, "w": true,
}

// phonemeObservation là một lần âm vị được chấm.
type phonemeObservation struct {
	Phoneme    string
	Accuracy   *float64
	Diagnosis  *string
	IsOmission bool
}

// score quy một lần chấm về điểm 0–100 để đưa vào EWMA, và cho biết có tính hay không.
//
// Ba trường hợp cần tách bạch:
//
//   - `omission` → 0. Âm vị KHÔNG được phát ra là thất bại nặng nhất với chính âm đó, và
//     nuốt phụ âm cuối là lỗi phổ biến nhất của người học Việt. Lưu ý điều này KHÔNG mâu
//     thuẫn với migration 000007 (nơi omission bị loại khỏi `accuracy` mức câu): ở đó
//     `completeness` đã phạt rồi nên tính thêm là phạt kép. Ở đây ta hỏi câu khác — "người
//     này phát âm /t/ tốt đến đâu" — và nuốt mất /t/ chính là câu trả lời.
//
//   - `insertion` → bỏ. Âm thừa không thuộc về âm vị chuẩn nào nên không quy được cho ai.
//
//   - `uncertain` → TÍNH, dùng accuracy của nó. Bất định nằm ở NHÃN (engine không chắc
//     nghe thấy gì), không ở ĐIỂM (GOP vẫn tính bình thường). Đây đúng là quy tắc mà
//     `aggregate.mean_accuracy` của engine áp dụng; lệch khỏi nó ở đây sẽ khiến điểm trong
//     Coach không khớp điểm người dùng vừa nhìn thấy sau khi đọc.
func (o phonemeObservation) score() (float64, bool) {
	diag := ""
	if o.Diagnosis != nil {
		diag = *o.Diagnosis
	}

	switch diag {
	case "insertion":
		return 0, false
	case "omission":
		return 0, true
	case "":
		// Bản ghi cũ hơn migration 000007 chưa có `diagnosis`; ngữ nghĩa omission khi đó
		// nằm ở cờ `is_omission`. Đọc được cả hai để lịch sử không bị bỏ trắng.
		if o.IsOmission {
			return 0, true
		}
	}

	if o.Accuracy == nil {
		return 0, false
	}
	return *o.Accuracy, true
}

// ewma tính trung bình có trọng số giảm dần theo thời gian, CHUẨN HOÁ theo tổng trọng số.
//
// Đầu vào theo thứ tự cũ → mới.
//
// Chuẩn hoá (tức EWMA có hiệu chỉnh chệch) chứ không phải công thức đệ quy quen thuộc
// `acc = α·v + (1−α)·acc` khởi tạo bằng quan sát đầu. Dạng quen thuộc đó có một lỗi thật
// khi số mẫu còn ít: quan sát CŨ NHẤT giữ trọng số (1−α)^(n−1), nên với 4 lần đọc thì lần
// đầu tiên vẫn nắm 34%. Hệ quả đo được — chuỗi tiến bộ [20,20,20,90] cho 41 điểm còn chuỗi
// sa sút [90,20,20,20] cho 44 điểm, tức người đang tệ đi lại được chấm cao hơn người đang
// khá lên. Test `TestEWMAWeightsRecentHigher` giữ cho lỗi đó không quay lại.
//
// Dạng chuẩn hoá luôn nằm trong [min, max] của các quan sát, không có hiện tượng giá trị
// khởi điểm áp đảo, và trùng với EWMA thường khi n lớn.
func ewma(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	const decay = 1 - ewmaAlpha
	var num, den float64
	for _, v := range values {
		num = num*decay + v
		den = den*decay + 1
	}
	return num / den
}

// overallScore là điểm tổng: trung bình mastery trên TOÀN BỘ âm vị đã luyện.
//
// Định nghĩa nằm ở một chỗ duy nhất vì nó xuất hiện ở hai nơi người dùng nhìn thấy cùng
// lúc — con số trong Coach và đường biểu đồ tiến bộ. Hai nơi tính khác nhau thì biểu đồ sẽ
// mâu thuẫn với chính con số ngay bên cạnh nó.
//
// Trước đây Coach tính trung bình trên danh sách hiển thị `ORDER BY mastery ASC LIMIT 20`,
// tức trung bình của 20 âm YẾU NHẤT. Tiếng Anh có ~44 âm vị nên với người luyện rộng, điểm
// tổng bị chặn trên bởi cái đuôi yếu và không bao giờ phản ánh phần họ đã làm tốt.
func overallScore(mastery map[string]masteryValue) *float64 {
	if len(mastery) == 0 {
		return nil // chưa luyện gì thì KHÔNG phải 0 điểm — chưa có gì để chấm
	}
	sum := 0.0
	for _, m := range mastery {
		sum += m.Mastery
	}
	avg := sum / float64(len(mastery))
	return &avg
}

func masteryStatus(mastery float64) string {
	switch {
	case mastery >= masteryGoodAtLeast:
		return "good"
	case mastery >= masteryWeakBelow:
		return "improving"
	default:
		return "weak"
	}
}

// ErrorProfileRecomputer tính lại hồ sơ lỗi của một người học.
//
// TÍNH LẠI TOÀN BỘ trong cửa sổ, không cộng dồn tăng tiến. Đây là quyết định có chủ đích:
// task được đẩy theo kiểu fire-and-forget và asynq có retry, nên một task chạy hai lần là
// chuyện bình thường. Cộng dồn EWMA hai lần sẽ đếm trùng và làm lệch mastery vĩnh viễn mà
// không có cách nào phát hiện. Tính lại thì chạy bao nhiêu lần cũng ra một kết quả.
type ErrorProfileRecomputer struct {
	db *pgxpool.Pool
}

// NewErrorProfileRecomputer tạo recomputer.
func NewErrorProfileRecomputer(db *pgxpool.Pool) *ErrorProfileRecomputer {
	return &ErrorProfileRecomputer{db: db}
}

// Recompute tính lại phoneme_mastery, skill_mastery và top_errors cho một user.
func (r *ErrorProfileRecomputer) Recompute(ctx context.Context, userID uuid.UUID) error {
	profileID, err := r.ensureProfile(ctx, userID)
	if err != nil {
		return err
	}

	phonemeMastery, err := r.computePhonemeMastery(ctx, userID)
	if err != nil {
		return err
	}
	skills, err := r.computeSkillMastery(ctx, userID)
	if err != nil {
		return err
	}
	pairs, err := r.computePairMastery(ctx, userID)
	if err != nil {
		return err
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("mở transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck — no-op sau Commit

	for phoneme, m := range phonemeMastery {
		if _, err := tx.Exec(ctx,
			`INSERT INTO phoneme_mastery
			   (error_profile_id, phoneme, mastery, attempts, status, last_practiced_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, now())
			 ON CONFLICT (error_profile_id, phoneme) DO UPDATE
			   SET mastery = EXCLUDED.mastery,
			       attempts = EXCLUDED.attempts,
			       status = EXCLUDED.status,
			       last_practiced_at = EXCLUDED.last_practiced_at,
			       updated_at = now()`,
			profileID, phoneme, m.Mastery, m.Attempts, masteryStatus(m.Mastery), m.LastPracticed,
		); err != nil {
			return fmt.Errorf("ghi phoneme_mastery %q: %w", phoneme, err)
		}
	}

	for skill, mastery := range skills {
		if _, err := tx.Exec(ctx,
			`INSERT INTO skill_mastery (error_profile_id, skill, mastery, status, updated_at)
			 VALUES ($1, $2, $3, $4, now())
			 ON CONFLICT (error_profile_id, skill) DO UPDATE
			   SET mastery = EXCLUDED.mastery,
			       status = EXCLUDED.status,
			       updated_at = now()`,
			profileID, skill, mastery, masteryStatus(mastery),
		); err != nil {
			return fmt.Errorf("ghi skill_mastery %q: %w", skill, err)
		}
	}

	for pairID, m := range pairs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO pair_mastery
			   (error_profile_id, minimal_pair_id, listen_mastery, speak_mastery,
			    attempts, status, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, now())
			 ON CONFLICT (error_profile_id, minimal_pair_id) DO UPDATE
			   SET listen_mastery = EXCLUDED.listen_mastery,
			       speak_mastery  = EXCLUDED.speak_mastery,
			       attempts       = EXCLUDED.attempts,
			       status         = EXCLUDED.status,
			       updated_at     = now()`,
			profileID, pairID, m.Listen, m.Speak, m.Attempts,
			masteryStatus((m.Listen+m.Speak)/2),
		); err != nil {
			return fmt.Errorf("ghi pair_mastery %s: %w", pairID, err)
		}
	}

	topErrors, err := json.Marshal(buildTopErrors(phonemeMastery))
	if err != nil {
		return fmt.Errorf("marshal top_errors: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE error_profiles
		    SET top_errors = $2::jsonb, last_recomputed_at = now(), updated_at = now()
		  WHERE id = $1`,
		profileID, string(topErrors),
	); err != nil {
		return fmt.Errorf("ghi top_errors: %w", err)
	}

	// Chụp ảnh trong CÙNG transaction với mastery: biểu đồ tiến bộ và con số trong Coach
	// là hai cách nhìn của một trạng thái. Tách ra hai lần ghi thì một lần hỏng sẽ để lại
	// biểu đồ mâu thuẫn với chính con số nằm ngay cạnh nó, và không ai phát hiện.
	if err := snapshotToday(ctx, tx, userID, phonemeMastery, skills); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit recompute: %w", err)
	}

	// SAU commit, không trong transaction: điều kiện trao huy hiệu đọc chính những bảng
	// vừa ghi (`phoneme_mastery`, `pair_mastery`), nên phải thấy dữ liệu đã commit.
	//
	// Lỗi ở đây chỉ log: hồ sơ lỗi đã được ghi thành công rồi, và trả lỗi sẽ khiến asynq
	// chạy lại toàn bộ phép tính chỉ vì phần thưởng phụ. Lần luyện sau sẽ trao bù, vì hàm
	// này trao mọi huy hiệu còn thiếu chứ không chỉ huy hiệu vừa đạt.
	if err := AwardBadges(ctx, r.db, userID); err != nil {
		slog.Error("trao huy hiệu sau khi tính hồ sơ lỗi", "user_id", userID, "err", err)
	}
	return nil
}

// snapshotToday ghi trạng thái mastery hôm nay vào `mastery_snapshots`.
//
// CHỈ chụp vào ngày người học thực sự luyện — chính là lúc hàm này được gọi. Ngày không
// luyện thì mastery không đổi, nên một dòng snapshot khi đó là BẢN SAO chứ không phải phép
// đo; ghi nó xuống là bịa ra dữ liệu và làm biểu đồ trông như có hoạt động.
//
// `UNIQUE (user_id, snapshot_date)` + upsert: luyện nhiều lần trong ngày thì lần cuối
// thắng, và task chạy lại không tạo dòng trùng.
func snapshotToday(
	ctx context.Context,
	tx pgx.Tx,
	userID uuid.UUID,
	phonemes map[string]masteryValue,
	skills map[string]float64,
) error {
	// Ngày theo múi giờ CỦA NGƯỜI HỌC, không theo UTC.
	//
	// Việt Nam là UTC+7: người luyện lúc 2 giờ sáng sẽ bị ghi vào ngày UTC hôm trước, và
	// biểu đồ ngày của họ lệch một ô. `users.timezone` đã có sẵn nên không phải đoán.
	var snapshotDate time.Time
	if err := tx.QueryRow(ctx,
		`SELECT (now() AT TIME ZONE COALESCE(NULLIF(timezone, ''), 'UTC'))::date
		   FROM users WHERE id = $1`,
		userID).Scan(&snapshotDate); err != nil {
		return fmt.Errorf("lấy ngày theo múi giờ người dùng: %w", err)
	}

	// Dạng gọn phoneme → mastery. Đủ để vẽ xu hướng và so hai mốc thời gian; `attempts`
	// và `status` suy lại được, không cần nhân bản vào từng ảnh chụp mỗi ngày.
	phonemeJSON := make(map[string]float64, len(phonemes))
	for phoneme, m := range phonemes {
		phonemeJSON[phoneme] = m.Mastery
	}

	phonemeBlob, err := json.Marshal(phonemeJSON)
	if err != nil {
		return fmt.Errorf("marshal phoneme_mastery: %w", err)
	}
	skillBlob, err := json.Marshal(skills)
	if err != nil {
		return fmt.Errorf("marshal skill_mastery: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO mastery_snapshots
		   (user_id, snapshot_date, overall_score, phoneme_mastery, skill_mastery)
		 VALUES ($1, $2, $3, $4::jsonb, $5::jsonb)
		 ON CONFLICT (user_id, snapshot_date) DO UPDATE
		   SET overall_score   = EXCLUDED.overall_score,
		       phoneme_mastery = EXCLUDED.phoneme_mastery,
		       skill_mastery   = EXCLUDED.skill_mastery,
		       created_at      = now()`,
		userID, snapshotDate, overallScore(phonemes), string(phonemeBlob), string(skillBlob),
	); err != nil {
		return fmt.Errorf("ghi mastery_snapshot: %w", err)
	}
	return nil
}

func (r *ErrorProfileRecomputer) ensureProfile(
	ctx context.Context, userID uuid.UUID,
) (uuid.UUID, error) {
	var id uuid.UUID
	// Người dùng đăng ký trước khi bảng này tồn tại sẽ không có dòng nào; tạo tại chỗ thay
	// vì lỗi, để một lần thiếu dữ liệu cũ không chặn vĩnh viễn hồ sơ của họ.
	err := r.db.QueryRow(ctx,
		`INSERT INTO error_profiles (user_id) VALUES ($1)
		 ON CONFLICT (user_id) DO UPDATE SET updated_at = now()
		 RETURNING id`,
		userID).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("lấy error_profile: %w", err)
	}
	return id, nil
}

type masteryValue struct {
	Mastery       float64
	Attempts      int
	LastPracticed *time.Time
}

func (r *ErrorProfileRecomputer) computePhonemeMastery(
	ctx context.Context, userID uuid.UUID,
) (map[string]masteryValue, error) {
	// Lấy `maxObservationsPerPhoneme` lần chấm gần nhất của TỪNG âm vị, rồi trả về theo
	// thứ tự cũ → mới để EWMA chạy đúng chiều thời gian.
	rows, err := r.db.Query(ctx,
		`SELECT phoneme, accuracy, diagnosis, is_omission, created_at
		   FROM (
		     SELECT ps.expected_phoneme AS phoneme,
		            ps.accuracy, ps.diagnosis, ps.is_omission, ps.created_at,
		            row_number() OVER (
		              PARTITION BY ps.expected_phoneme
		              ORDER BY ps.created_at DESC, ps.id DESC
		            ) AS rn
		       FROM phoneme_scores ps
		       JOIN practice_item_results pir ON ps.item_result_id = pir.id
		       JOIN practice_sessions s ON pir.session_id = s.id
		      WHERE s.user_id = $1
		        AND pir.trust_flag <> 'rejected'
		        AND ps.expected_phoneme <> ''
		        AND (ps.diagnosis IS NULL OR ps.diagnosis <> 'insertion')
		   ) t
		  WHERE rn <= $2
		  ORDER BY phoneme, created_at ASC`,
		userID, maxObservationsPerPhoneme)
	if err != nil {
		return nil, fmt.Errorf("đọc phoneme_scores: %w", err)
	}
	defer rows.Close()

	series := map[string][]float64{}
	last := map[string]time.Time{}
	for rows.Next() {
		var obs phonemeObservation
		var createdAt time.Time
		if err := rows.Scan(
			&obs.Phoneme, &obs.Accuracy, &obs.Diagnosis, &obs.IsOmission, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan phoneme_score: %w", err)
		}
		value, counted := obs.score()
		if !counted {
			continue
		}
		series[obs.Phoneme] = append(series[obs.Phoneme], value)
		if createdAt.After(last[obs.Phoneme]) {
			last[obs.Phoneme] = createdAt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("duyệt phoneme_scores: %w", err)
	}

	out := make(map[string]masteryValue, len(series))
	for phoneme, values := range series {
		practiced := last[phoneme]
		out[phoneme] = masteryValue{
			Mastery:       ewma(values),
			Attempts:      len(values),
			LastPracticed: &practiced,
		}
	}
	return out, nil
}

// computeSkillMastery tính các kỹ năng ĐO ĐƯỢC.
//
// `prosody` và `fluency` cố ý KHÔNG được ghi: engine v1 trả null cho cả hai (xem
// CAPABILITIES trong assess.py). Ghi 0 vào đó sẽ hiện lên UI thành "kỹ năng rất yếu" trong
// khi thực tế là "chưa đo". Vắng mặt trung thực hơn một con số bịa.
func (r *ErrorProfileRecomputer) computeSkillMastery(
	ctx context.Context, userID uuid.UUID,
) (map[string]float64, error) {
	rows, err := r.db.Query(ctx,
		`SELECT accuracy, completeness
		   FROM (
		     SELECT pir.accuracy, pir.completeness, pir.created_at
		       FROM practice_item_results pir
		       JOIN practice_sessions s ON pir.session_id = s.id
		      WHERE s.user_id = $1 AND pir.trust_flag <> 'rejected'
		      ORDER BY pir.created_at DESC
		      LIMIT $2
		   ) t
		  ORDER BY created_at ASC`,
		userID, maxItemObservations)
	if err != nil {
		return nil, fmt.Errorf("đọc practice_item_results: %w", err)
	}
	defer rows.Close()

	var accuracy, completeness []float64
	for rows.Next() {
		var acc, comp *float64
		if err := rows.Scan(&acc, &comp); err != nil {
			return nil, fmt.Errorf("scan item_result: %w", err)
		}
		if acc != nil {
			accuracy = append(accuracy, *acc)
		}
		if comp != nil {
			completeness = append(completeness, *comp)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("duyệt item_results: %w", err)
	}

	out := map[string]float64{}
	if len(accuracy) > 0 {
		out["accuracy"] = ewma(accuracy)
	}
	if len(completeness) > 0 {
		out["completeness"] = ewma(completeness)
	}

	finalConsonant, err := r.computeFinalConsonantMastery(ctx, userID)
	if err != nil {
		return nil, err
	}
	if finalConsonant != nil {
		out["final_consonant"] = *finalConsonant
	}
	return out, nil
}

// computeFinalConsonantMastery đo riêng phụ âm ĐỨNG CUỐI TỪ.
//
// Tách khỏi mastery từng âm vị vì đây là lỗi theo VỊ TRÍ, không theo âm: người học phát âm
// /t/ đầu từ rất tốt vẫn có thể nuốt sạch /t/ cuối từ. Gộp chung hai vị trí sẽ làm điểm
// trung bình che mất đúng lỗi cần chỉ ra.
func (r *ErrorProfileRecomputer) computeFinalConsonantMastery(
	ctx context.Context, userID uuid.UUID,
) (*float64, error) {
	rows, err := r.db.Query(ctx,
		`SELECT phoneme, accuracy, diagnosis, is_omission
		   FROM (
		     SELECT ps.expected_phoneme AS phoneme,
		            ps.accuracy, ps.diagnosis, ps.is_omission, ps.created_at,
		            row_number() OVER (
		              PARTITION BY ps.item_result_id, ps.word_index
		              ORDER BY ps.phoneme_index DESC
		            ) AS rn_in_word,
		            row_number() OVER (ORDER BY ps.created_at DESC, ps.id DESC) AS rn
		       FROM phoneme_scores ps
		       JOIN practice_item_results pir ON ps.item_result_id = pir.id
		       JOIN practice_sessions s ON pir.session_id = s.id
		      WHERE s.user_id = $1
		        AND pir.trust_flag <> 'rejected'
		        AND ps.word_index IS NOT NULL
		        AND ps.phoneme_index IS NOT NULL
		        AND ps.expected_phoneme <> ''
		        AND (ps.diagnosis IS NULL OR ps.diagnosis <> 'insertion')
		   ) t
		  WHERE rn_in_word = 1 AND rn <= $2
		  ORDER BY created_at ASC`,
		userID, maxItemObservations)
	if err != nil {
		return nil, fmt.Errorf("đọc phụ âm cuối: %w", err)
	}
	defer rows.Close()

	var values []float64
	for rows.Next() {
		var obs phonemeObservation
		if err := rows.Scan(
			&obs.Phoneme, &obs.Accuracy, &obs.Diagnosis, &obs.IsOmission,
		); err != nil {
			return nil, fmt.Errorf("scan phụ âm cuối: %w", err)
		}
		if !englishConsonants[obs.Phoneme] {
			continue
		}
		if value, counted := obs.score(); counted {
			values = append(values, value)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("duyệt phụ âm cuối: %w", err)
	}

	if len(values) == 0 {
		return nil, nil
	}
	m := ewma(values)
	return &m, nil
}

// pairMasteryValue giữ hai kỹ năng tách biệt của một cặp âm.
type pairMasteryValue struct {
	Listen   float64
	Speak    float64
	Attempts int
}

// computePairMastery tính tiến độ từng cặp âm dễ nhầm.
//
// NGHE và NÓI tách riêng vì đó là hai kỹ năng khác nhau và hỏng độc lập: người học có thể
// nghe ra khác biệt /l/–/n/ rất rõ mà vẫn không phát âm được, hoặc ngược lại. Gộp thành một
// con số sẽ che mất đúng kỹ năng cần luyện.
//
//   - `listen` từ `mp_listen_answers`: tỷ lệ đúng, quy về thang 0–100 để cùng thang với
//     mọi mastery khác.
//   - `speak` từ `practice_item_results.minimal_pair_id`: accuracy của những lần đọc chính
//     cặp đó.
//
// Người mới chỉ luyện một trong hai kỹ năng thì kỹ năng kia bằng 0 — điều này KHÔNG sai:
// `status` lấy trung bình hai vế nên chưa luyện nói thì cặp đó chưa thể "good", và đó đúng
// là điều muốn nói.
func (r *ErrorProfileRecomputer) computePairMastery(
	ctx context.Context, userID uuid.UUID,
) (map[string]pairMasteryValue, error) {
	listen := map[string][]float64{}
	rows, err := r.db.Query(ctx,
		`SELECT minimal_pair_id, is_correct
		   FROM (
		     SELECT a.minimal_pair_id, a.is_correct, a.created_at,
		            row_number() OVER (
		              PARTITION BY a.minimal_pair_id
		              ORDER BY a.created_at DESC, a.id DESC
		            ) AS rn
		       FROM mp_listen_answers a
		       JOIN mp_listen_drills d ON a.drill_id = d.id
		      WHERE d.user_id = $1 AND a.is_correct IS NOT NULL
		   ) t
		  WHERE rn <= $2
		  ORDER BY created_at ASC`,
		userID, maxObservationsPerPhoneme)
	if err != nil {
		return nil, fmt.Errorf("đọc kết quả nghe: %w", err)
	}
	for rows.Next() {
		var pairID string
		var correct bool
		if err := rows.Scan(&pairID, &correct); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan kết quả nghe: %w", err)
		}
		score := 0.0
		if correct {
			score = 100.0
		}
		listen[pairID] = append(listen[pairID], score)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("duyệt kết quả nghe: %w", err)
	}

	speak := map[string][]float64{}
	rows, err = r.db.Query(ctx,
		`SELECT minimal_pair_id, accuracy
		   FROM (
		     SELECT pir.minimal_pair_id, pir.accuracy, pir.created_at,
		            row_number() OVER (
		              PARTITION BY pir.minimal_pair_id
		              ORDER BY pir.created_at DESC, pir.id DESC
		            ) AS rn
		       FROM practice_item_results pir
		       JOIN practice_sessions s ON pir.session_id = s.id
		      WHERE s.user_id = $1
		        AND pir.minimal_pair_id IS NOT NULL
		        AND pir.accuracy IS NOT NULL
		        AND pir.trust_flag <> 'rejected'
		   ) t
		  WHERE rn <= $2
		  ORDER BY created_at ASC`,
		userID, maxObservationsPerPhoneme)
	if err != nil {
		return nil, fmt.Errorf("đọc kết quả nói cặp âm: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var pairID string
		var accuracy float64
		if err := rows.Scan(&pairID, &accuracy); err != nil {
			return nil, fmt.Errorf("scan kết quả nói: %w", err)
		}
		speak[pairID] = append(speak[pairID], accuracy)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("duyệt kết quả nói: %w", err)
	}

	out := map[string]pairMasteryValue{}
	for pairID, values := range listen {
		out[pairID] = pairMasteryValue{Listen: ewma(values), Attempts: len(values)}
	}
	for pairID, values := range speak {
		m := out[pairID]
		m.Speak = ewma(values)
		m.Attempts += len(values)
		out[pairID] = m
	}
	return out, nil
}

// buildTopErrors chọn các âm yếu nhất để Fix Guide và Coach bám vào.
func buildTopErrors(mastery map[string]masteryValue) []TopError {
	candidates := make([]TopError, 0, len(mastery))
	for phoneme, m := range mastery {
		if m.Attempts < minAttemptsForTopError || m.Mastery >= masteryWeakBelow {
			continue
		}
		candidates = append(candidates, TopError{
			Phoneme: phoneme,
			Mastery: m.Mastery,
			Status:  masteryStatus(m.Mastery),
			// L1Tag để trống: nội dung chọn theo lỗi ĐO ĐƯỢC, không theo tiếng mẹ đẻ khai
			// báo. Xem §6.1.0 của PRONUNCIATION_ENGINE_PLAN.md.
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Mastery != candidates[j].Mastery {
			return candidates[i].Mastery < candidates[j].Mastery
		}
		// Thứ tự phụ theo tên để kết quả ổn định giữa các lần chạy — map trong Go duyệt
		// ngẫu nhiên, thiếu cái này thì hai lần recompute giống hệt nhau vẫn sinh ra
		// top_errors khác thứ tự và trông như dữ liệu đang nhảy.
		return candidates[i].Phoneme < candidates[j].Phoneme
	})
	if len(candidates) > topErrorsLimit {
		candidates = candidates[:topErrorsLimit]
	}
	return candidates
}

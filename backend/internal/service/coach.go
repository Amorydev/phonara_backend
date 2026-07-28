package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

// ErrorProfileDTO is the coach profile response.
type ErrorProfileDTO struct {
	OverallScore         *float64        `json:"overall_score,omitempty"`
	MinimalPairsProgress *float64        `json:"minimal_pairs_progress,omitempty"`
	TopErrors            []TopError      `json:"top_errors"`
	PhonemeStatus        []PhonemeStatus `json:"phoneme_status"`
	SkillStatus          []SkillStatus   `json:"skill_status"`
}

// TopError is a summary of a frequent error.
type TopError struct {
	Phoneme string  `json:"phoneme"`
	Mastery float64 `json:"mastery"`
	Status  string  `json:"status"`
	L1Tag   *string `json:"l1_tag,omitempty"`
}

// PhonemeStatus is the mastery state for a phoneme.
type PhonemeStatus struct {
	Phoneme  string  `json:"phoneme"`
	Mastery  float64 `json:"mastery"`
	Attempts int     `json:"attempts"`
	Status   string  `json:"status"`
}

// SkillStatus is the mastery state for a skill (prosody, fluency, etc.).
type SkillStatus struct {
	Skill   string  `json:"skill"`
	Mastery float64 `json:"mastery"`
	Status  string  `json:"status"`
}

// CoachService handles Error Profile and recommendation logic.
type CoachService struct {
	db  *pgxpool.Pool
	rdb *goredis.Client
}

// NewCoachService creates a CoachService.
func NewCoachService(db *pgxpool.Pool, rdb *goredis.Client) *CoachService {
	return &CoachService{db: db, rdb: rdb}
}

// GetErrorProfile retrieves the user's error profile with mastery data.
func (s *CoachService) GetErrorProfile(ctx context.Context, userID uuid.UUID) (*ErrorProfileDTO, error) {
	// Get top_errors from denormalized column
	var topErrorsJSON []byte
	err := s.db.QueryRow(ctx,
		`SELECT ep.top_errors
		 FROM error_profiles ep
		 WHERE ep.user_id = $1`,
		userID).Scan(&topErrorsJSON)
	if err != nil {
		return nil, fmt.Errorf("get error profile: %w", err)
	}

	var topErrors []TopError
	if len(topErrorsJSON) > 0 {
		_ = json.Unmarshal(topErrorsJSON, &topErrors)
	}

	// Get phoneme mastery
	phonemeRows, err := s.db.Query(ctx,
		`SELECT pm.phoneme, pm.mastery, pm.attempts, pm.status
		 FROM phoneme_mastery pm
		 JOIN error_profiles ep ON pm.error_profile_id = ep.id
		 WHERE ep.user_id = $1
		 ORDER BY pm.mastery ASC
		 LIMIT 20`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("get phoneme mastery: %w", err)
	}
	defer phonemeRows.Close()

	var phonemeStatus []PhonemeStatus
	for phonemeRows.Next() {
		ps := PhonemeStatus{}
		if err := phonemeRows.Scan(&ps.Phoneme, &ps.Mastery, &ps.Attempts, &ps.Status); err != nil {
			return nil, fmt.Errorf("scan phoneme status: %w", err)
		}
		phonemeStatus = append(phonemeStatus, ps)
	}

	// Get skill mastery
	skillRows, err := s.db.Query(ctx,
		`SELECT sm.skill, sm.mastery, sm.status
		 FROM skill_mastery sm
		 JOIN error_profiles ep ON sm.error_profile_id = ep.id
		 WHERE ep.user_id = $1`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("get skill mastery: %w", err)
	}
	defer skillRows.Close()

	var skillStatus []SkillStatus
	for skillRows.Next() {
		ss := SkillStatus{}
		if err := skillRows.Scan(&ss.Skill, &ss.Mastery, &ss.Status); err != nil {
			return nil, fmt.Errorf("scan skill status: %w", err)
		}
		skillStatus = append(skillStatus, ss)
	}

	// Điểm tổng tính trên TOÀN BỘ âm vị, bằng một truy vấn riêng.
	//
	// KHÔNG tính trung bình trên `phonemeStatus`: danh sách đó là `ORDER BY mastery ASC
	// LIMIT 20`, tức 20 âm YẾU NHẤT, dựng ra để hiển thị. Tiếng Anh có ~44 âm vị nên dùng
	// lại nó làm điểm tổng sẽ chặn trên điểm của người luyện rộng bằng chính cái đuôi yếu
	// của họ. Đây cũng phải khớp với `overall_score` trong `mastery_snapshots`, nếu không
	// biểu đồ tiến bộ sẽ mâu thuẫn với con số nằm ngay cạnh nó.
	var overall *float64
	if err := s.db.QueryRow(ctx,
		`SELECT AVG(pm.mastery)
		   FROM phoneme_mastery pm
		   JOIN error_profiles ep ON pm.error_profile_id = ep.id
		  WHERE ep.user_id = $1`,
		userID).Scan(&overall); err != nil {
		return nil, fmt.Errorf("tính điểm tổng: %w", err)
	}
	overallScore := overall

	var minimalPairsProgress *float64
	if err := s.db.QueryRow(ctx,
		`SELECT AVG((pm.listen_mastery + pm.speak_mastery) / 2.0)
		 FROM pair_mastery pm
		 JOIN error_profiles ep ON pm.error_profile_id = ep.id
		 WHERE ep.user_id = $1`,
		userID,
	).Scan(&minimalPairsProgress); err != nil {
		return nil, fmt.Errorf("get minimal pairs progress: %w", err)
	}

	return &ErrorProfileDTO{
		OverallScore:         overallScore,
		MinimalPairsProgress: minimalPairsProgress,
		TopErrors:            topErrors,
		PhonemeStatus:        phonemeStatus,
		SkillStatus:          skillStatus,
	}, nil
}

// GetRecommendation returns ranked content recommendations based on error profile.
// rank = severity × frequency × L1_importance × goal_fit (BR-COACH-04)
func (s *CoachService) GetRecommendation(ctx context.Context, userID uuid.UUID) ([]map[string]any, error) {
	// Get weak phonemes
	rows, err := s.db.Query(ctx,
		`SELECT pm.phoneme
		 FROM phoneme_mastery pm
		 JOIN error_profiles ep ON pm.error_profile_id = ep.id
		 WHERE ep.user_id = $1 AND pm.status = 'weak'
		 ORDER BY pm.mastery ASC
		 LIMIT 5`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("get weak phonemes: %w", err)
	}
	defer rows.Close()

	phonemes := make([]string, 0, 5)
	for rows.Next() {
		var ph string
		if err := rows.Scan(&ph); err != nil {
			return nil, fmt.Errorf("scan phoneme: %w", err)
		}
		phonemes = append(phonemes, ph)
	}

	if len(phonemes) == 0 {
		// No weak phonemes yet — recommend default content
		return []map[string]any{
			{"type": "word", "reason": "complete_onboarding", "message": "Complete your first practice session to get personalized recommendations"},
		}, nil
	}

	// Find content items matching weak phonemes
	contentRows, err := s.db.Query(ctx,
		`SELECT DISTINCT ON (ci.id) ci.id, ci.type, ci.text, ci.difficulty
		 FROM content_items ci
		 WHERE ci.is_active = TRUE
		   AND ci.focus_phonemes && $1::text[]
		 ORDER BY ci.id, ci.difficulty ASC
		 LIMIT 10`,
		phonemes)
	if err != nil {
		return nil, fmt.Errorf("get recommended content: %w", err)
	}
	defer contentRows.Close()

	recs := make([]map[string]any, 0)
	for contentRows.Next() {
		var id, itemType, text string
		var difficulty int
		if err := contentRows.Scan(&id, &itemType, &text, &difficulty); err != nil {
			return nil, fmt.Errorf("scan recommendation: %w", err)
		}
		recs = append(recs, map[string]any{
			"content_id": id,
			"type":       itemType,
			"text":       text,
			"difficulty": difficulty,
			"phoneme":    phonemes[0],
			"reason":     "weak_phoneme",
		})
	}

	return recs, nil
}

// GetReport returns a weekly or monthly progress report.
func (s *CoachService) GetReport(ctx context.Context, userID uuid.UUID, period string) (map[string]any, error) {
	limit := 7
	if period == "month" {
		limit = 30
	}

	rows, err := s.db.Query(ctx,
		`SELECT snapshot_date, overall_score
		 FROM mastery_snapshots
		 WHERE user_id = $1
		 ORDER BY snapshot_date DESC
		 LIMIT $2`,
		userID, limit)
	if err != nil {
		return nil, fmt.Errorf("get report: %w", err)
	}
	defer rows.Close()

	snapshots := make([]map[string]any, 0, limit)
	for rows.Next() {
		var date string
		var score *float64
		if err := rows.Scan(&date, &score); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		snapshots = append(snapshots, map[string]any{
			"date":  date,
			"score": score,
		})
	}

	return map[string]any{
		"period":    period,
		"snapshots": snapshots,
	}, nil
}

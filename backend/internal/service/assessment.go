package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phonara/backend/internal/pkg/apperrors"
)

// AssessmentFilter selects which assessment set to return.
// All fields are optional; empty values fall back to the active default set.
type AssessmentFilter struct {
	Code  string // specific set slug, e.g. "pre_assessment_default"
	Level string // CEFR level filter, e.g. "A2"
}

// AssessmentSet is the DTO returned when starting an assessment. It bundles the
// set metadata together with every question so the client fetches everything in
// a single request.
type AssessmentSet struct {
	ID            string                `json:"id"`
	Code          string                `json:"code"`
	Type          string                `json:"type"`
	Title         string                `json:"title"`
	Description   *string               `json:"description,omitempty"`
	CEFRLevel     *string               `json:"cefr_level,omitempty"`
	Locale        string                `json:"locale"`
	QuestionCount int                   `json:"question_count"`
	Questions     []*AssessmentQuestion `json:"questions"`
}

// AssessmentQuestion is one sentence the user reads aloud.
type AssessmentQuestion struct {
	ID               string  `json:"id"`
	Order            int     `json:"order"`
	Text             string  `json:"text"`
	Phonetic         *string `json:"phonetic,omitempty"`
	SampleAudioURL   *string `json:"sample_audio_url,omitempty"`
	ExpectedDuration *int    `json:"expected_duration,omitempty"`
	Difficulty       *int    `json:"difficulty,omitempty"`
}

// AssessmentService retrieves onboarding assessment question sets.
type AssessmentService struct {
	db *pgxpool.Pool
}

// NewAssessmentService creates an AssessmentService.
func NewAssessmentService(db *pgxpool.Pool) *AssessmentService {
	return &AssessmentService{db: db}
}

// GetPreAssessment returns the active pre-assessment set with all its questions.
// When the filter is empty it resolves the highest-version active default set;
// callers may pin a specific set via Code or narrow by CEFR Level.
func (s *AssessmentService) GetPreAssessment(ctx context.Context, f AssessmentFilter) (*AssessmentSet, error) {
	set := &AssessmentSet{Questions: []*AssessmentQuestion{}}

	err := s.db.QueryRow(ctx,
		// Không có code → lấy bộ ĐƯỢC ĐÁNH DẤU mặc định, không phải bộ version cao nhất.
		// Dựa vào version thì một ngày ai đó bump version bộ benchmark (23 câu) và nó
		// lặng lẽ thành bộ onboarding — người dùng mới phải đọc 5 phút mà không ai biết
		// vì sao. Ưu tiên phải tường minh.
		`SELECT id, code, type, title, description, cefr_level, locale
		   FROM assessment_sets
		  WHERE type = 'pre_assessment'
		    AND is_active = TRUE
		    AND ($1 = '' OR code = $1)
		    AND ($1 <> '' OR is_default)
		    AND ($2 = '' OR cefr_level = $2)
		  ORDER BY version DESC, created_at DESC
		  LIMIT 1`,
		f.Code, f.Level,
	).Scan(&set.ID, &set.Code, &set.Type, &set.Title, &set.Description, &set.CEFRLevel, &set.Locale)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("get assessment set: %w", err)
	}

	rows, err := s.db.Query(ctx,
		`SELECT id, order_index, text, phonetic, sample_audio_url, expected_duration, difficulty
		   FROM assessment_questions
		  WHERE set_id = $1 AND is_active = TRUE
		  ORDER BY order_index`,
		set.ID)
	if err != nil {
		return nil, fmt.Errorf("list assessment questions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		q := &AssessmentQuestion{}
		if err := rows.Scan(&q.ID, &q.Order, &q.Text, &q.Phonetic,
			&q.SampleAudioURL, &q.ExpectedDuration, &q.Difficulty); err != nil {
			return nil, fmt.Errorf("scan assessment question: %w", err)
		}
		set.Questions = append(set.Questions, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate assessment questions: %w", err)
	}

	set.QuestionCount = len(set.Questions)
	return set, nil
}

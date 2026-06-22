package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phonara/backend/internal/config"
)

// DailyService handles daily challenges.
type DailyService struct {
	db *pgxpool.Pool
}

// NewDailyService creates a DailyService.
func NewDailyService(db *pgxpool.Pool) *DailyService {
	return &DailyService{db: db}
}

// Today returns today's daily challenge.
func (s *DailyService) Today(ctx context.Context, userID uuid.UUID) (map[string]any, error) {
	today := time.Now().UTC().Format("2006-01-02")
	var id, category string
	var passageID, contentID, bannerURL *string
	var moderated bool

	err := s.db.QueryRow(ctx,
		`SELECT id, passage_id, content_item_id, category, banner_url, moderated
		 FROM daily_challenges WHERE date = $1`,
		today).Scan(&id, &passageID, &contentID, &category, &bannerURL, &moderated)
	if err != nil {
		return map[string]any{
			"available": false,
			"message":   "No challenge available today",
		}, nil
	}

	var status *string
	s.db.QueryRow(ctx,
		`SELECT status FROM daily_challenge_completions WHERE user_id = $1 AND challenge_id = $2`,
		userID, id).Scan(&status)

	return map[string]any{
		"challenge_id":    id,
		"date":            today,
		"passage_id":      passageID,
		"content_item_id": contentID,
		"category":        category,
		"banner_url":      bannerURL,
		"moderated":       moderated,
		"user_status":     status,
	}, nil
}

// History returns daily challenge history for the user.
func (s *DailyService) History(ctx context.Context, userID uuid.UUID) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx,
		`SELECT dc.date, dc.category, dcc.status, dcc.score, dcc.completed_at
		 FROM daily_challenge_completions dcc
		 JOIN daily_challenges dc ON dcc.challenge_id = dc.id
		 WHERE dcc.user_id = $1
		 ORDER BY dc.date DESC
		 LIMIT 30`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("get daily history: %w", err)
	}
	defer rows.Close()

	items := make([]map[string]any, 0)
	for rows.Next() {
		var date, category, status string
		var score *float64
		var completedAt *time.Time
		if err := rows.Scan(&date, &category, &status, &score, &completedAt); err != nil {
			return nil, fmt.Errorf("scan daily history: %w", err)
		}
		items = append(items, map[string]any{
			"date": date, "category": category, "status": status,
			"score": score, "completed_at": completedAt,
		})
	}
	return items, nil
}

// ExamService handles exam sessions and Speechace scoring.
type ExamService struct {
	db  *pgxpool.Pool
	cfg *config.Config
}

// NewExamService creates an ExamService.
func NewExamService(db *pgxpool.Pool, cfg *config.Config) *ExamService {
	return &ExamService{db: db, cfg: cfg}
}

// ListPrompts returns active exam prompts.
func (s *ExamService) ListPrompts(ctx context.Context, examType, part string) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, exam_type, part, prompt_text, prep_seconds, speak_seconds
		 FROM exam_prompts
		 WHERE is_active = TRUE
		   AND ($1 = '' OR exam_type = $1)
		   AND ($2 = '' OR part = $2)
		 ORDER BY exam_type, part`,
		examType, part)
	if err != nil {
		return nil, fmt.Errorf("list prompts: %w", err)
	}
	defer rows.Close()

	prompts := make([]map[string]any, 0)
	for rows.Next() {
		var id, et, p, text string
		var prep, speak int
		if err := rows.Scan(&id, &et, &p, &text, &prep, &speak); err != nil {
			return nil, fmt.Errorf("scan prompt: %w", err)
		}
		prompts = append(prompts, map[string]any{
			"id": id, "exam_type": et, "part": p,
			"prompt_text": text, "prep_seconds": prep, "speak_seconds": speak,
		})
	}
	return prompts, nil
}

// Create opens a new exam session.
func (s *ExamService) Create(ctx context.Context, userID uuid.UUID, promptID string) (map[string]any, error) {
	var examType string
	err := s.db.QueryRow(ctx,
		`SELECT exam_type FROM exam_prompts WHERE id = $1`, promptID).Scan(&examType)
	if err != nil {
		return nil, fmt.Errorf("get prompt: %w", err)
	}

	sessionID := uuid.New()
	_, err = s.db.Exec(ctx,
		`INSERT INTO exam_sessions (id, user_id, prompt_id, exam_type, status)
		 VALUES ($1, $2, $3, $4, 'submitted')`,
		sessionID, userID, promptID, examType)
	if err != nil {
		return nil, fmt.Errorf("create exam session: %w", err)
	}

	return map[string]any{
		"session_id": sessionID, "exam_type": examType, "status": "submitted",
	}, nil
}

// Submit uploads audio_ref and enqueues async Speechace scoring.
func (s *ExamService) Submit(ctx context.Context, userID uuid.UUID, sessionID, audioRef string) (map[string]any, error) {
	_, err := s.db.Exec(ctx,
		`UPDATE exam_sessions SET audio_ref = $1, status = 'scoring'
		 WHERE id = $2 AND user_id = $3`,
		audioRef, sessionID, userID)
	if err != nil {
		return nil, fmt.Errorf("submit exam: %w", err)
	}
	// TODO: enqueue asynq task TypeExamScoring
	return map[string]any{"session_id": sessionID, "status": "scoring"}, nil
}

// Report returns the scored exam report.
func (s *ExamService) Report(ctx context.Context, userID uuid.UUID, sessionID string) (map[string]any, error) {
	var examType, status string
	var bandScore *float64
	var cefrLevel *string
	var criteria any

	err := s.db.QueryRow(ctx,
		`SELECT exam_type, status, band_score, cefr_level, criteria
		 FROM exam_sessions WHERE id = $1 AND user_id = $2`,
		sessionID, userID).Scan(&examType, &status, &bandScore, &cefrLevel, &criteria)
	if err != nil {
		return nil, fmt.Errorf("get exam report: %w", err)
	}

	return map[string]any{
		"session_id": sessionID, "exam_type": examType, "status": status,
		"band_score": bandScore, "cefr_level": cefrLevel, "criteria": criteria,
	}, nil
}

// ListSessions returns exam session history.
func (s *ExamService) ListSessions(ctx context.Context, userID uuid.UUID) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, exam_type, status, band_score, cefr_level, created_at, scored_at
		 FROM exam_sessions WHERE user_id = $1
		 ORDER BY created_at DESC LIMIT 20`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("list exam sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]map[string]any, 0)
	for rows.Next() {
		var id, examType, status string
		var bandScore *float64
		var cefrLevel *string
		var createdAt time.Time
		var scoredAt *time.Time
		if err := rows.Scan(&id, &examType, &status, &bandScore, &cefrLevel, &createdAt, &scoredAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, map[string]any{
			"id": id, "exam_type": examType, "status": status,
			"band_score": bandScore, "cefr_level": cefrLevel,
			"created_at": createdAt, "scored_at": scoredAt,
		})
	}
	return sessions, nil
}

// SystemService handles system-level operations.
type SystemService struct {
	db *pgxpool.Pool
}

// NewSystemService creates a SystemService.
func NewSystemService(db *pgxpool.Pool) *SystemService {
	return &SystemService{db: db}
}

// SubmitFeedback stores a user feedback or bug report.
func (s *SystemService) SubmitFeedback(ctx context.Context, userID uuid.UUID, feedbackType, message string, ctxData map[string]any) error {
	ctxJSON := "{}"
	if ctxData != nil {
		if b, err := json.Marshal(ctxData); err == nil {
			ctxJSON = string(b)
		}
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO feedback_reports (user_id, type, message, context)
		 VALUES ($1, $2, $3, $4::jsonb)`,
		userID, feedbackType, message, ctxJSON)
	if err != nil {
		return fmt.Errorf("submit feedback: %w", err)
	}
	return nil
}

// GetAppConfig retrieves all app config flags.
func (s *SystemService) GetAppConfig(ctx context.Context) (map[string]any, error) {
	rows, err := s.db.Query(ctx,
		`SELECT key, value FROM app_configs ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("get app config: %w", err)
	}
	defer rows.Close()

	cfg := make(map[string]any)
	for rows.Next() {
		var key string
		var value any
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan config: %w", err)
		}
		cfg[key] = value
	}
	return cfg, nil
}

// GetLegalDoc retrieves the latest legal document of the given type.
func (s *SystemService) GetLegalDoc(ctx context.Context, docType string) (map[string]any, error) {
	var id, content, locale string
	var version int
	err := s.db.QueryRow(ctx,
		`SELECT id, version, content_md, locale
		 FROM legal_documents
		 WHERE doc_type = $1 AND published_at IS NOT NULL
		 ORDER BY version DESC LIMIT 1`,
		docType).Scan(&id, &version, &content, &locale)
	if err != nil {
		return nil, fmt.Errorf("get legal doc %s: %w", docType, err)
	}
	return map[string]any{
		"id": id, "doc_type": docType, "version": version,
		"content_md": content, "locale": locale,
	}, nil
}

// IngestEvent stores an analytics event.
func (s *SystemService) IngestEvent(ctx context.Context, userID uuid.UUID, eventName string, properties map[string]any) error {
	propsJSON := "{}"
	if properties != nil {
		if b, err := json.Marshal(properties); err == nil {
			propsJSON = string(b)
		}
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO analytics_events (user_id, event_name, properties)
		 VALUES ($1, $2, $3::jsonb)`,
		userID, eventName, propsJSON)
	if err != nil {
		return fmt.Errorf("ingest event: %w", err)
	}
	return nil
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phonara/backend/internal/config"
	"github.com/phonara/backend/internal/pkg/apperrors"
)

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

// Submit lưu bản ghi của bài thi. CHẤM ĐIỂM CHƯA CÓ.
//
// Vì sao pronunciation-engine KHÔNG chấm được bài thi: engine tính GOP bằng cách so âm
// thanh với chuỗi âm vị CHUẨN suy từ `reference_text`. Bài thi là nói tự do theo đề
// (`exam_prompts.prompt_text` + `speak_seconds`) — không có văn bản chuẩn nào để so. Đây
// là giới hạn kiến trúc, không phải việc chưa làm xong: muốn chấm thì phải có ASR để chuyển
// lời nói thành văn bản trước, hoặc dùng dịch vụ chấm thi bên ngoài.
//
// Trước đây hàm này đặt `status = 'scoring'` rồi trả về "scoring" trong khi KHÔNG có gì
// chấm cả — bài thi treo mãi ở trạng thái đó và client poll vô hạn không bao giờ có kết
// quả. Giữ `status = 'submitted'` và báo lỗi tường minh thì người dùng biết ngay.
//
// Audio VẪN được lưu: khi tính năng chấm có mặt, những bài đã nộp chấm lại được.
func (s *ExamService) Submit(ctx context.Context, userID uuid.UUID, sessionID, audioRef string) (map[string]any, error) {
	if _, err := s.db.Exec(ctx,
		`UPDATE exam_sessions SET audio_ref = $1
		  WHERE id = $2 AND user_id = $3`,
		audioRef, sessionID, userID,
	); err != nil {
		return nil, fmt.Errorf("lưu bản ghi bài thi: %w", err)
	}

	return nil, apperrors.New(
		http.StatusServiceUnavailable,
		"exam scoring is not available yet; your recording has been saved",
		apperrors.ErrServiceUnavail,
	)
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

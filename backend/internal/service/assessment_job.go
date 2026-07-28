package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phonara/backend/internal/domain"
	"github.com/phonara/backend/internal/integration/storage"
	"github.com/phonara/backend/internal/pkg/apperrors"
)

// Trạng thái job chấm phát âm.
const (
	JobPending    = "pending"
	JobProcessing = "processing"
	JobDone       = "done"
	JobFailed     = "failed"
)

// TypeAssessmentRun là task type asynq cho việc chấm.
const TypeAssessmentRun = "assessment:run"

// AssessmentJobPayload là payload task.
type AssessmentJobPayload struct {
	JobID string `json:"job_id"`
}

// CreateAssessmentJobInput là tham số tạo job.
type CreateAssessmentJobInput struct {
	UserID               uuid.UUID
	ClientIP             string
	SessionID            string
	ReferenceText        string
	ScoringLevel         string
	ContentItemID        string
	MinimalPairID        string
	AssessmentQuestionID string
	IdempotencyKey       string
	Audio                []byte
}

// AssessmentJobDTO là biểu diễn công khai của job.
//
// Result chỉ có khi Status == done; Error chỉ có khi Status == failed. Client poll cùng
// một endpoint và phân nhánh theo Status.
type AssessmentJobDTO struct {
	ID        uuid.UUID                          `json:"id"`
	Status    string                             `json:"status"`
	CreatedAt time.Time                          `json:"created_at"`
	Result    *domain.NormalizedAssessmentResult `json:"result,omitempty"`
	Error     *domain.EngineError                `json:"error,omitempty"`
}

// AssessmentJobService quản lý vòng đời job chấm phát âm.
type AssessmentJobService struct {
	db      *pgxpool.Pool
	store   storage.Store
	enqueue *asynq.Client
	gate    *PracticeGate
}

// NewAssessmentJobService tạo service.
func NewAssessmentJobService(
	db *pgxpool.Pool, store storage.Store, enqueue *asynq.Client, gate *PracticeGate,
) *AssessmentJobService {
	return &AssessmentJobService{db: db, store: store, enqueue: enqueue, gate: gate}
}

// Create lưu audio, tạo job, đẩy vào hàng đợi.
//
// Idempotency: cùng IdempotencyKey trả về job cũ thay vì tạo mới. Client mất mạng giữa
// chừng rồi thử lại sẽ không tạo bản ghi trùng và không tốn thêm một lần inference.
func (s *AssessmentJobService) Create(
	ctx context.Context, in CreateAssessmentJobInput,
) (*AssessmentJobDTO, error) {
	// Kiểm idempotency TRƯỚC gate: thử lại cùng một bản ghi không được tính thêm lượt,
	// nếu không thì mất mạng giữa chừng sẽ khiến người dùng bị trừ hai lần cho một lần đọc.
	if in.IdempotencyKey != "" {
		if existing, err := s.findByIdempotencyKey(ctx, in.UserID, in.IdempotencyKey); err != nil {
			return nil, err
		} else if existing != nil {
			return existing, nil
		}
	}

	// Rate limit + quota freemium + độ sâu hàng đợi. Không có lớp này thì luồng mới lách
	// sạch mọi phòng thủ mà /v1/speech/token từng có.
	if s.gate != nil {
		if err := s.gate.Allow(ctx, in.UserID, in.ClientIP); err != nil {
			return nil, err
		}
	}
	quotaConsumed := s.gate != nil
	refundQuota := func() {
		if quotaConsumed {
			compensationCtx, cancel := context.WithTimeout(
				context.WithoutCancel(ctx), 5*time.Second,
			)
			s.gate.Refund(compensationCtx, in.UserID)
			cancel()
			quotaConsumed = false
		}
	}

	jobID := uuid.New()

	// Lưu audio TRƯỚC khi tạo job: nếu tạo job trước rồi lưu audio hỏng, ta có một job
	// pending trỏ tới audio không tồn tại và worker sẽ retry vô ích ba lần.
	audioRef, err := s.store.Put(ctx, storage.Key(in.UserID.String(), jobID.String()), in.Audio)
	if err != nil {
		refundQuota()
		return nil, fmt.Errorf("lưu audio: %w", err)
	}

	_, err = s.db.Exec(ctx,
		`INSERT INTO assessment_jobs
		   (id, user_id, session_id, status, audio_ref, reference_text, scoring_level,
		    content_item_id, minimal_pair_id, assessment_question_id, idempotency_key)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		jobID, in.UserID, nullIfEmpty(in.SessionID), JobPending, audioRef,
		in.ReferenceText, orMedium(in.ScoringLevel),
		nullIfEmpty(in.ContentItemID), nullIfEmpty(in.MinimalPairID),
		nullIfEmpty(in.AssessmentQuestionID), nullIfEmpty(in.IdempotencyKey),
	)
	if err != nil {
		// Dọn audio mồ côi — không có job nào trỏ tới nó nữa.
		compensationCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), 5*time.Second,
		)
		deleteErr := s.store.Delete(compensationCtx, audioRef)
		cancel()
		refundQuota()
		if in.IdempotencyKey != "" {
			existing, findErr := s.findByIdempotencyKey(ctx, in.UserID, in.IdempotencyKey)
			if findErr == nil && existing != nil {
				return existing, nil
			}
		}
		if deleteErr != nil {
			return nil, errors.Join(
				fmt.Errorf("tạo assessment job: %w", err),
				fmt.Errorf("dọn audio sau lỗi: %w", deleteErr),
			)
		}
		return nil, fmt.Errorf("tạo assessment job: %w", err)
	}

	payload, err := json.Marshal(AssessmentJobPayload{JobID: jobID.String()})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	task := asynq.NewTask(TypeAssessmentRun, payload, asynq.Queue("critical"))
	if _, err := s.enqueue.EnqueueContext(ctx, task,
		asynq.MaxRetry(3), asynq.Timeout(60*time.Second),
	); err != nil {
		cleanupErr := s.cleanupUnqueuedJob(ctx, jobID, audioRef)
		refundQuota()
		if cleanupErr != nil {
			return nil, errors.Join(
				fmt.Errorf("đẩy task vào hàng đợi: %w", err),
				cleanupErr,
			)
		}
		return nil, fmt.Errorf("đẩy task vào hàng đợi: %w", err)
	}

	return &AssessmentJobDTO{ID: jobID, Status: JobPending, CreatedAt: time.Now()}, nil
}

func (s *AssessmentJobService) cleanupUnqueuedJob(
	ctx context.Context, jobID uuid.UUID, audioRef string,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	var errs []error
	if _, err := s.db.Exec(cleanupCtx, `DELETE FROM assessment_jobs WHERE id = $1`, jobID); err != nil {
		errs = append(errs, fmt.Errorf("xóa job chưa enqueue: %w", err))
	}
	if err := s.store.Delete(cleanupCtx, audioRef); err != nil {
		errs = append(errs, fmt.Errorf("xóa audio chưa enqueue: %w", err))
	}
	return errors.Join(errs...)
}

// Get trả về job kèm kết quả hoặc lỗi, kiểm quyền sở hữu.
func (s *AssessmentJobService) Get(
	ctx context.Context, userID uuid.UUID, jobID string,
) (*AssessmentJobDTO, error) {
	var (
		dto       AssessmentJobDTO
		rawResult []byte
		errCode   *string
		errMsg    *string
	)
	err := s.db.QueryRow(ctx,
		`SELECT id, status, created_at, raw_result, error_code, error_message
		   FROM assessment_jobs WHERE id = $1 AND user_id = $2`,
		jobID, userID,
	).Scan(&dto.ID, &dto.Status, &dto.CreatedAt, &rawResult, &errCode, &errMsg)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lấy assessment job: %w", err)
	}

	if dto.Status == JobDone && len(rawResult) > 0 {
		var result domain.NormalizedAssessmentResult
		if err := json.Unmarshal(rawResult, &result); err != nil {
			return nil, fmt.Errorf("parse raw_result: %w", err)
		}
		dto.Result = &result
	}
	if dto.Status == JobFailed && errCode != nil {
		engErr := &domain.EngineError{
			Code:    domain.EngineErrorCode(*errCode),
			Message: derefOr(errMsg, ""),
		}
		// Chỉ lộ thông điệp cho user khi lỗi là do họ (nói quá ngắn, không có tiếng).
		// Lỗi hệ thống thì giấu chi tiết — user không làm gì được với nó, và message
		// nội bộ có thể lộ cấu trúc hệ thống.
		if !engErr.IsUserFacing() {
			engErr.Message = "không chấm được bản ghi này, vui lòng thử lại"
		}
		dto.Error = engErr
	}
	return &dto, nil
}

func (s *AssessmentJobService) findByIdempotencyKey(
	ctx context.Context, userID uuid.UUID, key string,
) (*AssessmentJobDTO, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT id FROM assessment_jobs WHERE idempotency_key = $1 AND user_id = $2`,
		key, userID,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("kiểm idempotency: %w", err)
	}
	return s.Get(ctx, userID, id.String())
}

func orMedium(level string) string {
	if level == "" {
		return "medium"
	}
	return level
}

func derefOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

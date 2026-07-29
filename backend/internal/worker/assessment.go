package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phonara/backend/internal/domain"
	"github.com/phonara/backend/internal/integration/speech"
	"github.com/phonara/backend/internal/integration/storage"
	"github.com/phonara/backend/internal/service"
)

// HandleTaskError closes the externally visible job lifecycle when Asynq has
// exhausted all retries. Without this transition, the task moves to archived
// while the API keeps reporting "processing" forever.
func HandleTaskError(
	ctx context.Context, db *pgxpool.Pool, store storage.Store, task *asynq.Task,
) {
	if task.Type() != TypeAssessmentRun {
		return
	}
	retried, okRetry := asynq.GetRetryCount(ctx)
	maxRetry, okMax := asynq.GetMaxRetry(ctx)
	if !okRetry || !okMax || !retryExhausted(retried, maxRetry) {
		return
	}

	var payload service.AssessmentJobPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return
	}
	jobID, err := uuid.Parse(payload.JobID)
	if err != nil {
		return
	}

	finalizeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	job, loadErr := loadJob(finalizeCtx, db, jobID.String())
	markFailed(
		finalizeCtx,
		db,
		jobID,
		domain.EngErrInternal,
		"assessment failed after retry limit",
	)
	if loadErr == nil && !job.retainRecording && store != nil {
		_ = store.Delete(finalizeCtx, job.audioRef)
	}
}

func retryExhausted(retried, maxRetry int) bool {
	return retried >= maxRetry
}

// handleAssessmentRun chấm một bản ghi: tải audio → gọi engine → chuẩn hoá → lưu.
//
// Ngữ nghĩa retry: trả lỗi thường thì asynq retry; bọc asynq.SkipRetry thì dừng hẳn.
// Lỗi VĨNH VIỄN (audio quá ngắn, không có tiếng) không bao giờ được retry — bản ghi sẽ
// không tự tốt lên, và mỗi lần thử lại đốt thêm một lần inference.
func handleAssessmentRun(
	db *pgxpool.Pool, store storage.Store, engine *speech.Client, gate *service.PracticeGate,
	engineGate *EngineGate,
) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p service.AssessmentJobPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal payload: %w: %w", err, asynq.SkipRetry)
		}

		job, err := loadJob(ctx, db, p.JobID)
		if err != nil {
			return err
		}
		if job.status == service.JobDone || job.status == service.JobFailed {
			if !job.retainRecording {
				if err := store.Delete(ctx, job.audioRef); err != nil {
					return fmt.Errorf("xóa audio không được phép lưu: %w", err)
				}
			}
			// Terminal job delivered again: only finish pending privacy cleanup.
			return nil
		}

		if _, err := db.Exec(ctx,
			`UPDATE assessment_jobs
			    SET status = $1, started_at = COALESCE(started_at, now()),
			        attempts = attempts + 1
			  WHERE id = $2`,
			service.JobProcessing, job.id,
		); err != nil {
			return fmt.Errorf("đánh dấu processing: %w", err)
		}

		audio, err := store.Get(ctx, job.audioRef)
		if err != nil {
			// Audio mất thì retry cũng vô ích.
			markFailed(ctx, db, job.id, domain.EngErrInternal, "audio không đọc được")
			return fmt.Errorf("đọc audio %s: %w: %w", job.audioRef, err, asynq.SkipRetry)
		}

		// Giữ suất inference SAU khi đã tải audio: tải audio không cần suất, và ôm suất
		// trong lúc chờ mạng S3 là lãng phí đúng tài nguyên khan hiếm nhất trong hệ thống.
		var result *domain.NormalizedAssessmentResult
		err = func() error {
			if acquireErr := engineGate.Acquire(ctx); acquireErr != nil {
				return acquireErr
			}
			// defer chứ không gọi thẳng: Asynq bắt panic trong handler, nên một suất rò rỉ
			// sẽ làm giảm vĩnh viễn sức chứa mà không ai thấy.
			defer engineGate.Release()

			var callErr error
			result, callErr = engine.Assess(ctx, speech.AssessInput{
				Audio:         audio,
				ReferenceText: job.referenceText,
				RequestID:     job.id.String(),
			})
			return callErr
		}()

		if errors.Is(err, ErrEngineBusy) || errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			// Chưa tới lượt, hoặc worker đang tắt. KHÔNG markFailed: job vẫn hợp lệ,
			// chưa tốn lượt inference nào, và client vẫn đang poll. RetryDelayFunc ở
			// cmd/worker đưa nó quay lại sau vài giây.
			slog.Warn("chưa giành được suất inference, sẽ thử lại",
				"job_id", job.id, "err", err)
			return fmt.Errorf("chờ suất inference: %w", err)
		}

		if err != nil {
			var engErr *domain.EngineError
			if errors.As(err, &engErr) && !engErr.Retryable {
				markFailed(ctx, db, job.id, engErr.Code, engErr.Message)
				// BR-SCORE-07: bản ghi hỏng không bị trừ điểm — và cũng không nên bị
				// trừ LƯỢT. Người học không nhận được gì thì không có lý do mất lượt,
				// nhất là khi lỗi thường do micro hoặc môi trường ồn.
				if gate != nil && service.ErrQuotaRefundable(string(engErr.Code)) {
					gate.Refund(ctx, job.userID)
					slog.Info("hoàn lượt do bản ghi không dùng được",
						"user_id", job.userID, "code", engErr.Code)
				}
				if !job.retainRecording {
					if deleteErr := store.Delete(ctx, job.audioRef); deleteErr != nil {
						return fmt.Errorf("xóa audio không được phép lưu: %w", deleteErr)
					}
				}
				slog.Info("assessment thất bại vĩnh viễn",
					"job_id", job.id, "code", engErr.Code)
				return fmt.Errorf("engine từ chối: %w: %w", err, asynq.SkipRetry)
			}
			// Tạm thời — để asynq retry. CHƯA đánh failed, vì lần sau có thể thành công
			// và client vẫn đang poll.
			slog.Warn("assessment lỗi tạm thời, sẽ thử lại", "job_id", job.id, "err", err)
			return fmt.Errorf("gọi engine: %w", err)
		}

		if err := persistResult(ctx, db, job, result); err != nil {
			return fmt.Errorf("lưu kết quả: %w", err)
		}
		if !job.retainRecording {
			if err := store.Delete(ctx, job.audioRef); err != nil {
				// The job is already done. A retry enters the terminal branch
				// above and performs only this privacy cleanup.
				return fmt.Errorf("xóa audio không được phép lưu: %w", err)
			}
		}

		slog.Info("assessment xong",
			"job_id", job.id,
			"engine", result.Engine,
			"phonemes", len(result.Phonemes),
			"total_ms", result.TimingMs.Total,
		)
		return nil
	}
}

type assessmentJob struct {
	id                   uuid.UUID
	userID               uuid.UUID
	sessionID            *string
	status               string
	audioRef             string
	referenceText        string
	scoringLevel         string
	contentItemID        *string
	minimalPairID        *string
	assessmentQuestionID *string
	retainRecording      bool
}

func loadJob(ctx context.Context, db *pgxpool.Pool, jobID string) (*assessmentJob, error) {
	var j assessmentJob
	err := db.QueryRow(ctx,
		`SELECT j.id, j.user_id, j.session_id, j.status, j.audio_ref,
		        j.reference_text, j.scoring_level, j.content_item_id,
		        j.minimal_pair_id, j.assessment_question_id,
		        u.consent_store_recordings
		   FROM assessment_jobs j
		   JOIN users u ON u.id = j.user_id
		  WHERE j.id = $1`,
		jobID,
	).Scan(&j.id, &j.userID, &j.sessionID, &j.status, &j.audioRef, &j.referenceText,
		&j.scoringLevel, &j.contentItemID, &j.minimalPairID, &j.assessmentQuestionID,
		&j.retainRecording)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("job %s không tồn tại: %w", jobID, asynq.SkipRetry)
	}
	if err != nil {
		return nil, fmt.Errorf("nạp job: %w", err)
	}
	return &j, nil
}

// persistResult lưu kết quả vào practice_item_results + phoneme_scores trong một
// transaction, rồi đánh dấu job done.
func persistResult(
	ctx context.Context, db *pgxpool.Pool, job *assessmentJob,
	r *domain.NormalizedAssessmentResult,
) error {
	rawJSON, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal raw_result: %w", err)
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	capabilities := make([]string, 0, len(r.Capabilities))
	for _, c := range r.Capabilities {
		capabilities = append(capabilities, string(c))
	}

	miscue, err := json.Marshal(buildMiscueFromResult(r))
	if err != nil {
		return fmt.Errorf("marshal miscue: %w", err)
	}

	// Chỉ ghi item_result khi job gắn với một session. Job rời (ví dụ thu dữ liệu
	// benchmark) vẫn lưu raw_result nhưng không làm bẩn thống kê luyện tập.
	var itemResultID *uuid.UUID
	if job.sessionID != nil {
		id := uuid.New()
		itemResultID = &id
		var persistedAudioRef *string
		if job.retainRecording {
			persistedAudioRef = &job.audioRef
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO practice_item_results
			   (id, session_id, content_item_id, minimal_pair_id, assessment_question_id,
			    assessment_job_id, scoring_level, accuracy, fluency, completeness, prosody,
			    miscue, audio_ref, trust_flag,
			    engine, model_version, g2p_version, algorithm_version, calibration_version,
			    capabilities)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
			id, *job.sessionID, job.contentItemID, job.minimalPairID,
			job.assessmentQuestionID, job.id, job.scoringLevel,
			r.Overall.Accuracy, r.Overall.Fluency, r.Overall.Completeness, r.Overall.Prosody,
			string(miscue), persistedAudioRef, "ok",
			r.Engine, r.ModelVersion, r.G2PVersion, r.AlgorithmVersion, r.CalibrationVersion,
			capabilities,
		); err != nil {
			return fmt.Errorf("insert item result: %w", err)
		}

		for _, ph := range r.Phonemes {
			if _, err := tx.Exec(ctx,
				`INSERT INTO phoneme_scores
				   (item_result_id, expected_phoneme, said_phoneme, accuracy,
				    word_index, phoneme_index, is_omission, diagnosis, confidence, gop_raw)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
				id, ph.Expected, ph.Said, ph.Accuracy,
				ph.WordIndex, ph.PhonemeIndex,
				ph.Diagnosis == domain.DiagOmission, // is_omission: DEPRECATED, dẫn xuất
				string(ph.Diagnosis), ph.Confidence, ph.GOPRaw,
			); err != nil {
				return fmt.Errorf("insert phoneme score: %w", err)
			}
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE assessment_jobs
		    SET status = $1, raw_result = $2::jsonb, completed_at = now(),
		        error_code = NULL, error_message = NULL
		  WHERE id = $3`,
		service.JobDone, string(rawJSON), job.id,
	); err != nil {
		return fmt.Errorf("đánh dấu done: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	_ = itemResultID
	return nil
}

// buildMiscueFromResult dựng payload miscue từ kết quả engine.
//
// GIỮ LẠI cả `uncertain`. Bỏ chúng đi sẽ khiến Fix Guide hiểu nhầm "engine không kết
// luận được" thành "user phát âm đúng" — đúng lỗi đã có trong code cũ.
func buildMiscueFromResult(r *domain.NormalizedAssessmentResult) []map[string]any {
	miscue := make([]map[string]any, 0, len(r.Words))
	for _, w := range r.Words {
		if w.Diagnosis == domain.WordCorrect {
			continue
		}
		miscue = append(miscue, map[string]any{
			"word":       w.Word,
			"error_type": string(w.Diagnosis),
			"accuracy":   w.Accuracy,
			"word_index": w.WordIndex,
		})
	}
	return miscue
}

func markFailed(
	ctx context.Context, db *pgxpool.Pool, jobID uuid.UUID,
	code domain.EngineErrorCode, message string,
) {
	if _, err := db.Exec(ctx,
		`UPDATE assessment_jobs
		    SET status = $1, error_code = $2, error_message = $3, completed_at = now()
			  WHERE id = $4 AND status <> $5`,
		service.JobFailed, string(code), message, jobID,
		service.JobDone,
	); err != nil {
		slog.Error("không đánh dấu được job failed", "job_id", jobID, "err", err)
	}
}

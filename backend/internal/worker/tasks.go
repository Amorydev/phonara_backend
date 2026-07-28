// Package worker defines asynq task types and registers handlers.
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phonara/backend/internal/integration/speech"
	"github.com/phonara/backend/internal/integration/storage"
	"github.com/phonara/backend/internal/integration/tts"
	"github.com/phonara/backend/internal/service"
)

// Task type constants.
const (
	TypeErrorProfileRecompute = "errorprofile:recompute"
	TypeTTSBatch              = "tts:batch"
	TypeAccountDelete         = "account:delete"
	TypeAccountExport         = "account:export"
	TypeIAPWebhook            = "iap:webhook"
	TypeNotification          = "notification:push"
	TypeMasterySnapshot       = "mastery:snapshot"
	TypeExamScoring           = "exam:scoring"
	// TypeAssessmentRun chấm một bản ghi qua pronunciation-engine.
	// Hằng số gốc ở service.TypeAssessmentRun; lặp lại ở đây để nhóm task type,
	// và có test khẳng định hai giá trị khớp nhau.
	TypeAssessmentRun = "assessment:run"
)

// RegisterHandlers mounts all task handlers on the mux.
func RegisterHandlers(
	mux *asynq.ServeMux,
	db *pgxpool.Pool,
	store storage.Store,
	engine *speech.Client,
	ttsProvider tts.Provider,
	enqueuer *asynq.Client,
	gate *service.PracticeGate,
) {
	mux.HandleFunc(TypeAssessmentRun, handleAssessmentRun(db, store, engine, gate))
	mux.HandleFunc(TypeErrorProfileRecompute, handleErrorProfileRecompute(db))
	mux.HandleFunc(TypeTTSBatch, handleTTSBatch(db, store, ttsProvider, enqueuer))
	mux.HandleFunc(TypeAccountDelete, handleAccountDelete(db))
	mux.HandleFunc(TypeAccountExport, handleAccountExport(db))
	mux.HandleFunc(TypeNotification, handleNotification(db))
	mux.HandleFunc(TypeMasterySnapshot, handleMasterySnapshot(db))
	mux.HandleFunc(TypeExamScoring, handleExamScoring(db))
}

func handleErrorProfileRecompute(db *pgxpool.Pool) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p service.ErrorProfilePayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal payload: %w", err)
		}
		userID, err := uuid.Parse(p.UserID)
		if err != nil {
			// Payload hỏng thì retry bao nhiêu lần cũng hỏng — báo SkipRetry để nó vào
			// hàng archived thay vì quay vòng tới hết MaxRetry.
			return fmt.Errorf("user_id %q không hợp lệ: %w: %w",
				p.UserID, err, asynq.SkipRetry)
		}

		started := time.Now()
		if err := service.NewErrorProfileRecomputer(db).Recompute(ctx, userID); err != nil {
			return fmt.Errorf("tính lại error profile: %w", err)
		}
		slog.Info("đã tính lại error profile",
			"user_id", p.UserID, "mất", time.Since(started).String())
		return nil
	}
}

// errNotImplemented báo tính năng chưa xây, kèm SkipRetry.
//
// Handler chưa xây phải THẤT BẠI, không được `return nil`.
//
// `return nil` khiến asynq đánh dấu task THÀNH CÔNG: không retry, không vào hàng archived,
// không một dòng log lỗi. Tính năng im lặng không làm gì, và không có cách nào phát hiện
// ngoài việc tự đi kiểm dữ liệu. Đó chính là cách `handleErrorProfileRecompute` nằm rỗng
// đủ lâu để bảng `phoneme_mastery` không có nổi một dòng.
//
// SkipRetry vì thiếu code thì thử lại 3 lần cũng vẫn thiếu — chỉ tổ làm nhiễu log.
func errNotImplemented(what string) error {
	return fmt.Errorf("%s chưa được xây — xem TODO trong internal/worker/tasks.go: %w",
		what, asynq.SkipRetry)
}

// handleAccountDelete — xoá cứng dữ liệu sau thời gian ân hạn.
//
// CHƯA XÂY, nhưng đường xoá CHÍNH đã hoạt động: `MeService.DeleteAccount` xoá bản ghi khỏi
// S3, đánh dấu `users.deleted_at` và thu hồi mọi phiên ngay khi người dùng bấm xoá. Task
// này dành cho việc xoá cứng bản ghi CSDL sau thời gian ân hạn — hiện chưa có ai đẩy nó.
func handleAccountDelete(db *pgxpool.Pool) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		slog.Error("xoá cứng tài khoản chưa được xây", "payload", string(t.Payload()))
		return errNotImplemented("xoá cứng tài khoản")
	}
}

// handleAccountExport — xuất dữ liệu cá nhân (GDPR).
//
// `MeService.EnqueueExport` đã trả 501 nên task này chưa bao giờ được đẩy. Giữ handler thất
// bại tường minh để nếu ai đó nối đường enqueue trước khi xây xong, lỗi sẽ hiện ra ngay
// thay vì để người dùng chờ một file không bao giờ tới.
func handleAccountExport(db *pgxpool.Pool) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		slog.Error("xuất dữ liệu cá nhân chưa được xây", "payload", string(t.Payload()))
		return errNotImplemented("xuất dữ liệu cá nhân")
	}
}

// handleNotification — đẩy thông báo qua FCM/APNs. Chưa xây.
func handleNotification(db *pgxpool.Pool) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		slog.Error("đẩy thông báo chưa được xây", "payload", string(t.Payload()))
		return errNotImplemented("đẩy thông báo")
	}
}

// handleMasterySnapshot là JOB SỬA CHỮA, không phải đường ghi chính.
//
// Đường chính là `snapshotToday` chạy trong cùng transaction với việc tính lại hồ sơ lỗi —
// ảnh chụp được ghi đúng lúc mastery thay đổi. Task này chỉ để vá những ngày mà việc tính
// lại thất bại hẳn sau khi hết retry: tìm người CÓ luyện hôm nay nhưng THIẾU ảnh chụp, rồi
// tính lại cho họ.
//
// Cố ý không chụp cho mọi người mỗi ngày: ngày không luyện thì mastery không đổi, nên dòng
// snapshot khi đó là bản sao chứ không phải phép đo — ghi xuống là bịa dữ liệu và làm biểu
// đồ trông như có hoạt động.
func handleMasterySnapshot(db *pgxpool.Pool) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		rows, err := db.Query(ctx,
			`SELECT DISTINCT s.user_id
			   FROM practice_sessions s
			   JOIN users u ON u.id = s.user_id
			  WHERE s.started_at >= now() - interval '2 days'
			    AND NOT EXISTS (
			          SELECT 1 FROM mastery_snapshots ms
			           WHERE ms.user_id = s.user_id
			             AND ms.snapshot_date =
			                 (now() AT TIME ZONE COALESCE(NULLIF(u.timezone, ''), 'UTC'))::date
			        )`)
		if err != nil {
			return fmt.Errorf("tìm người thiếu ảnh chụp: %w", err)
		}
		defer rows.Close()

		var userIDs []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("scan user_id: %w", err)
			}
			userIDs = append(userIDs, id)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("duyệt danh sách: %w", err)
		}

		recomputer := service.NewErrorProfileRecomputer(db)
		var failed int
		for _, id := range userIDs {
			// Một người hỏng không được chặn phần còn lại — nếu không, một hồ sơ lỗi sẽ
			// khiến cả job vá không bao giờ chạy hết.
			if err := recomputer.Recompute(ctx, id); err != nil {
				failed++
				slog.Error("vá ảnh chụp thất bại", "user_id", id, "err", err)
			}
		}

		slog.Info("vá ảnh chụp mastery xong",
			"thiếu", len(userIDs), "hỏng", failed)
		if failed > 0 {
			return fmt.Errorf("%d/%d người vá không thành công", failed, len(userIDs))
		}
		return nil
	}
}

// handleExamScoring — chấm bài thi nói. CHƯA XÂY, và không xây bằng engine hiện tại được.
//
// pronunciation-engine tính GOP bằng cách so âm thanh với chuỗi âm vị chuẩn suy từ
// `reference_text`. Bài thi là nói tự do theo đề nên không có văn bản chuẩn để so. Muốn
// chấm phải có ASR chuyển lời nói thành văn bản trước, hoặc dùng dịch vụ chấm bên ngoài —
// đây là giới hạn kiến trúc chứ không phải việc chưa làm xong.
//
// Đánh dấu bài thi 'failed' để nó không treo mãi ở 'scoring' nếu có ai đó nối đường enqueue.
func handleExamScoring(db *pgxpool.Pool) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal: %w: %w", err, asynq.SkipRetry)
		}
		if _, err := db.Exec(ctx,
			`UPDATE exam_sessions SET status = 'failed' WHERE id = $1 AND status = 'scoring'`,
			p.SessionID,
		); err != nil {
			slog.Error("đánh dấu bài thi thất bại", "session_id", p.SessionID, "err", err)
		}
		slog.Error("chấm bài thi chưa được xây", "session_id", p.SessionID)
		return errNotImplemented("chấm bài thi nói")
	}
}

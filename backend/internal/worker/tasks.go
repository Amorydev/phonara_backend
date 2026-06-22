// Package worker defines asynq task types and registers handlers.
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
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
)

// RegisterHandlers mounts all task handlers on the mux.
func RegisterHandlers(mux *asynq.ServeMux, db *pgxpool.Pool) {
	mux.HandleFunc(TypeErrorProfileRecompute, handleErrorProfileRecompute(db))
	mux.HandleFunc(TypeTTSBatch, handleTTSBatch(db))
	mux.HandleFunc(TypeAccountDelete, handleAccountDelete(db))
	mux.HandleFunc(TypeAccountExport, handleAccountExport(db))
	mux.HandleFunc(TypeNotification, handleNotification(db))
	mux.HandleFunc(TypeMasterySnapshot, handleMasterySnapshot(db))
	mux.HandleFunc(TypeExamScoring, handleExamScoring(db))
}

// ErrorProfilePayload is the task payload for error profile recompute.
type ErrorProfilePayload struct {
	UserID string `json:"user_id"`
}

// NewErrorProfileRecomputeTask creates a task to recompute an error profile.
func NewErrorProfileRecomputeTask(userID string) (*asynq.Task, error) {
	payload, err := json.Marshal(ErrorProfilePayload{UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return asynq.NewTask(TypeErrorProfileRecompute, payload, asynq.Queue("default")), nil
}

func handleErrorProfileRecompute(db *pgxpool.Pool) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p ErrorProfilePayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal payload: %w", err)
		}
		slog.Info("recomputing error profile", "user_id", p.UserID)
		// TODO: implement full EWMA recompute logic
		return nil
	}
}

func handleTTSBatch(db *pgxpool.Pool) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		slog.Info("processing TTS batch")
		// TODO: scan content_items without audio_url, call Azure TTS, upload S3
		return nil
	}
}

func handleAccountDelete(db *pgxpool.Pool) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p struct{ UserID string `json:"user_id"` }
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}
		slog.Info("hard-deleting account audio", "user_id", p.UserID)
		// TODO: query audio_refs for user, delete from S3, then hard-delete DB records
		return nil
	}
}

func handleAccountExport(db *pgxpool.Pool) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p struct{ UserID string `json:"user_id"` }
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}
		slog.Info("exporting account data", "user_id", p.UserID)
		// TODO: collect all user data, generate ZIP, upload S3, notify user
		return nil
	}
}

func handleNotification(db *pgxpool.Pool) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		slog.Info("sending push notification")
		// TODO: FCM/APNs push
		return nil
	}
}

func handleMasterySnapshot(db *pgxpool.Pool) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		slog.Info("taking mastery snapshot")
		// TODO: compute overall_score per user, insert mastery_snapshots
		return nil
	}
}

func handleExamScoring(db *pgxpool.Pool) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p struct{ SessionID string `json:"session_id"` }
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}
		slog.Info("scoring exam session", "session_id", p.SessionID)
		// TODO: get audio_ref from DB, call Speechace API, update band_score + cefr_level
		return nil
	}
}

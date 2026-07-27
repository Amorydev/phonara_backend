package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phonara/backend/internal/pkg/apperrors"
)

// UserProfile is the public user profile DTO.
type UserProfile struct {
	ID                  uuid.UUID `json:"id"`
	AuthProvider        string    `json:"auth_provider"`
	Email               *string   `json:"email,omitempty"`
	DisplayName         *string   `json:"display_name,omitempty"`
	AvatarURL           *string   `json:"avatar_url,omitempty"`
	Goal                *string   `json:"goal,omitempty"`
	Level               *string   `json:"level,omitempty"`
	DefaultScoringLevel string    `json:"default_scoring_level"`
	TargetAccent        string    `json:"target_accent"`
	DailyGoalItems      int       `json:"daily_goal_items"`
	DailyGoalMinutes    int       `json:"daily_goal_minutes"`
	Timezone            string    `json:"timezone"`
	IsGuest             bool      `json:"is_guest"`
	OnboardingCompleted bool      `json:"onboarding_completed"`
	CreatedAt           time.Time `json:"created_at"`
}

// NotifPrefs is notification preferences DTO.
type NotifPrefs struct {
	PracticeReminder struct {
		Enabled bool    `json:"enabled"`
		Time    *string `json:"time,omitempty"`
	} `json:"practice_reminder"`
	StreakReminder struct {
		Enabled bool `json:"enabled"`
	} `json:"streak_reminder"`
}

// PrivacyPrefs is privacy settings DTO.
type PrivacyPrefs struct {
	AllowRecordingForImprovement bool `json:"allow_recording_for_improvement"`
	SaveRecordingHistory         bool `json:"save_recording_history"`
}

// UpdateProfileInput holds mutable profile fields.
type UpdateProfileInput struct {
	Goal                string
	Level               string
	TargetAccent        string
	DailyGoalItems      *int
	DailyGoalMinutes    *int
	DefaultScoringLevel string
	DisplayName         *string
	AvatarURL           *string
	Timezone            *string
}

// MeService handles user profile business logic.
type MeService struct {
	db *pgxpool.Pool
}

// NewMeService creates a MeService.
func NewMeService(db *pgxpool.Pool) *MeService {
	return &MeService{db: db}
}

// GetProfile retrieves the user profile.
func (s *MeService) GetProfile(ctx context.Context, userID uuid.UUID) (*UserProfile, error) {
	row := s.db.QueryRow(ctx,
		`SELECT id, auth_provider, email, display_name, avatar_url, goal, level,
		        default_scoring_level, target_accent, daily_goal_items, daily_goal_minutes,
		        timezone, is_guest, onboarding_completed, created_at
		 FROM users
		 WHERE id = $1 AND deleted_at IS NULL`,
		userID)

	p := &UserProfile{}
	if err := row.Scan(
		&p.ID, &p.AuthProvider, &p.Email, &p.DisplayName, &p.AvatarURL,
		&p.Goal, &p.Level, &p.DefaultScoringLevel, &p.TargetAccent,
		&p.DailyGoalItems, &p.DailyGoalMinutes, &p.Timezone, &p.IsGuest,
		&p.OnboardingCompleted, &p.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("get profile: %w", apperrors.ErrNotFound)
	}
	return p, nil
}

// UpdateProfile updates mutable fields on the user profile.
func (s *MeService) UpdateProfile(ctx context.Context, userID uuid.UUID, in UpdateProfileInput) (*UserProfile, error) {
	_, err := s.db.Exec(ctx,
		`UPDATE users SET
		   goal                  = COALESCE(NULLIF($2,''), goal),
		   level                 = COALESCE(NULLIF($3,''), level),
		   target_accent         = COALESCE(NULLIF($4,''), target_accent),
		   daily_goal_items      = COALESCE($5, daily_goal_items),
		   default_scoring_level = COALESCE(NULLIF($6,''), default_scoring_level),
		   display_name          = COALESCE($7, display_name),
		   timezone              = COALESCE($8, timezone),
		   avatar_url            = COALESCE($9, avatar_url),
		   daily_goal_minutes    = COALESCE($10, daily_goal_minutes)
		 WHERE id = $1 AND deleted_at IS NULL`,
		userID, in.Goal, in.Level, in.TargetAccent,
		in.DailyGoalItems, in.DefaultScoringLevel, in.DisplayName, in.Timezone,
		in.AvatarURL, in.DailyGoalMinutes,
	)
	if err != nil {
		return nil, fmt.Errorf("update profile: %w", err)
	}
	return s.GetProfile(ctx, userID)
}

// DeleteAccount soft-deletes the user and enqueues async hard-delete of audio.
func (s *MeService) DeleteAccount(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE users SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`,
		userID)
	if err != nil {
		return fmt.Errorf("soft-delete account: %w", err)
	}
	// TODO: enqueue asynq task to hard-delete all audio_ref on S3
	return nil
}

// SyncData returns the user's sync payload for multi-device support.
func (s *MeService) SyncData(ctx context.Context, userID uuid.UUID) (map[string]any, error) {
	profile, err := s.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Return condensed sync payload; extend as more state is needed
	return map[string]any{
		"profile":   profile,
		"synced_at": time.Now().UTC(),
	}, nil
}

// GetNotifPrefs retrieves notification preferences.
func (s *MeService) GetNotifPrefs(ctx context.Context, userID uuid.UUID) (*NotifPrefs, error) {
	var (
		practiceEnabled bool
		practiceTime    *string
		streakEnabled   bool
	)
	err := s.db.QueryRow(ctx,
		`SELECT notif_practice_reminder,
		        TO_CHAR(reminder_time, 'HH24:MI'),
		        notif_streak_reminder
		 FROM users WHERE id = $1 AND deleted_at IS NULL`,
		userID).Scan(&practiceEnabled, &practiceTime, &streakEnabled)
	if err != nil {
		return nil, fmt.Errorf("get notif prefs: %w", err)
	}

	p := &NotifPrefs{}
	p.PracticeReminder.Enabled = practiceEnabled
	p.PracticeReminder.Time = practiceTime
	p.StreakReminder.Enabled = streakEnabled
	return p, nil
}

// UpdateNotifPrefs updates notification preferences.
func (s *MeService) UpdateNotifPrefs(ctx context.Context, userID uuid.UUID, req any) (*NotifPrefs, error) {
	// TODO: parse req fields selectively using reflection or type assertion
	// For now delegate back to GetNotifPrefs after no-op
	return s.GetNotifPrefs(ctx, userID)
}

// RegisterDevice registers a push token for a device.
func (s *MeService) RegisterDevice(ctx context.Context, userID uuid.UUID, pushToken, platform string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO user_devices (user_id, platform, push_token, last_seen_at)
		 VALUES ($1, $2, $3, now())
		 ON CONFLICT (user_id, push_token) DO UPDATE SET last_seen_at = now()`,
		userID, platform, pushToken)
	if err != nil {
		return fmt.Errorf("register device: %w", err)
	}
	return nil
}

// GetPrivacy retrieves privacy consent settings.
func (s *MeService) GetPrivacy(ctx context.Context, userID uuid.UUID) (*PrivacyPrefs, error) {
	p := &PrivacyPrefs{}
	err := s.db.QueryRow(ctx,
		`SELECT consent_improve_product, consent_store_recordings
		 FROM users WHERE id = $1 AND deleted_at IS NULL`,
		userID).Scan(&p.AllowRecordingForImprovement, &p.SaveRecordingHistory)
	if err != nil {
		return nil, fmt.Errorf("get privacy: %w", err)
	}
	return p, nil
}

// UpdatePrivacy updates privacy consent settings.
func (s *MeService) UpdatePrivacy(ctx context.Context, userID uuid.UUID, allowImprovement, saveHistory *bool) (*PrivacyPrefs, error) {
	_, err := s.db.Exec(ctx,
		`UPDATE users SET
		   consent_improve_product   = COALESCE($2, consent_improve_product),
		   consent_store_recordings  = COALESCE($3, consent_store_recordings)
		 WHERE id = $1 AND deleted_at IS NULL`,
		userID, allowImprovement, saveHistory)
	if err != nil {
		return nil, fmt.Errorf("update privacy: %w", err)
	}
	return s.GetPrivacy(ctx, userID)
}

// EnqueueExport enqueues a GDPR data export task.
func (s *MeService) EnqueueExport(ctx context.Context, userID uuid.UUID) error {
	// TODO: enqueue asynq task TypeAccountExport
	_ = ctx
	_ = userID
	return nil
}

// DeletePracticeHistory deletes practice sessions/results/audio for a user (keeps account).
func (s *MeService) DeletePracticeHistory(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM practice_sessions WHERE user_id = $1`,
		userID)
	if err != nil {
		return fmt.Errorf("delete practice history: %w", err)
	}
	return nil
}

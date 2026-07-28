package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

// StreakDTO holds streak information for a user.
type StreakDTO struct {
	CurrentStreak int    `json:"current_streak"`
	LongestStreak int    `json:"longest_streak"`
	LastActiveDate string `json:"last_active_date,omitempty"`
}

// StreakService handles streak tracking.
type StreakService struct {
	db  *pgxpool.Pool
	rdb *goredis.Client
}

// NewStreakService creates a StreakService.
func NewStreakService(db *pgxpool.Pool, rdb *goredis.Client) *StreakService {
	return &StreakService{db: db, rdb: rdb}
}

// CheckIn records a daily check-in for the user and updates streak.
func (s *StreakService) CheckIn(ctx context.Context, userID uuid.UUID) (*StreakDTO, error) {
	// Get user timezone
	var timezone string
	if err := s.db.QueryRow(ctx,
		`SELECT timezone FROM users WHERE id = $1`,
		userID).Scan(&timezone); err != nil {
		timezone = "Asia/Ho_Chi_Minh"
	}

	// Calculate today in user's timezone
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.FixedZone("UTC+7", 7*3600)
	}
	today := time.Now().In(loc).Format("2006-01-02")

	// Upsert streak
	var current, longest int
	err = s.db.QueryRow(ctx,
		`INSERT INTO streak_records (user_id, current_streak, longest_streak, last_active_date)
		 VALUES ($1, 1, 1, $2)
		 ON CONFLICT (user_id) DO UPDATE SET
		   current_streak = CASE
		     WHEN streak_records.last_active_date = ($2::date - 1)
		       THEN streak_records.current_streak + 1
		     WHEN streak_records.last_active_date = $2::date
		       THEN streak_records.current_streak  -- same day, no change
		     ELSE 1  -- streak broken
		   END,
		   longest_streak = GREATEST(streak_records.longest_streak,
		     CASE
		       WHEN streak_records.last_active_date = ($2::date - 1)
		         THEN streak_records.current_streak + 1
		       ELSE 1
		     END),
		   last_active_date = $2
		 RETURNING current_streak, longest_streak`,
		userID, today).Scan(&current, &longest)
	if err != nil {
		return nil, fmt.Errorf("check in streak: %w", err)
	}

	// Huy hiệu chuỗi ngày chỉ đổi ở đây — việc tính lại hồ sơ lỗi không chạm tới
	// `streak_records`, nên thiếu lời gọi này thì mọi huy hiệu `streak` vĩnh viễn khoá.
	//
	// Lỗi chỉ log: điểm danh đã thành công, và trả lỗi sẽ khiến người học tưởng chuỗi ngày
	// của mình bị mất.
	if err := AwardBadges(ctx, s.db, userID); err != nil {
		slog.Error("trao huy hiệu sau điểm danh", "user_id", userID, "err", err)
	}

	return &StreakDTO{
		CurrentStreak:  current,
		LongestStreak:  longest,
		LastActiveDate: today,
	}, nil
}

// ProgressService handles user progress.
type ProgressService struct {
	db *pgxpool.Pool
}

// NewProgressService creates a ProgressService.
func NewProgressService(db *pgxpool.Pool) *ProgressService {
	return &ProgressService{db: db}
}

// Overview returns a high-level progress overview.
func (s *ProgressService) Overview(ctx context.Context, userID uuid.UUID) (map[string]any, error) {
	var current, longest int
	var lastActive *string
	s.db.QueryRow(ctx,
		`SELECT current_streak, longest_streak, TO_CHAR(last_active_date, 'YYYY-MM-DD')
		 FROM streak_records WHERE user_id = $1`,
		userID).Scan(&current, &longest, &lastActive)

	var totalSessions int
	s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM practice_sessions WHERE user_id = $1 AND ended_at IS NOT NULL`,
		userID).Scan(&totalSessions)

	return map[string]any{
		"current_streak":  current,
		"longest_streak":  longest,
		"last_active_date": lastActive,
		"total_sessions":  totalSessions,
	}, nil
}

// Charts returns score trend data from mastery_snapshots.
func (s *ProgressService) Charts(ctx context.Context, userID uuid.UUID, period string) (map[string]any, error) {
	limit := 7
	if period == "month" {
		limit = 30
	}

	rows, err := s.db.Query(ctx,
		`SELECT TO_CHAR(snapshot_date, 'YYYY-MM-DD'), overall_score
		 FROM mastery_snapshots
		 WHERE user_id = $1
		 ORDER BY snapshot_date DESC
		 LIMIT $2`,
		userID, limit)
	if err != nil {
		return nil, fmt.Errorf("get charts: %w", err)
	}
	defer rows.Close()

	points := make([]map[string]any, 0, limit)
	for rows.Next() {
		var date string
		var score *float64
		if err := rows.Scan(&date, &score); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		points = append(points, map[string]any{"date": date, "score": score})
	}

	return map[string]any{"period": period, "points": points}, nil
}

// BadgeService handles the badge system.
type BadgeService struct {
	db *pgxpool.Pool
}

// NewBadgeService creates a BadgeService.
func NewBadgeService(db *pgxpool.Pool) *BadgeService {
	return &BadgeService{db: db}
}

// List returns all badges with earned/locked status for the user.
func (s *BadgeService) List(ctx context.Context, userID uuid.UUID) (map[string]any, error) {
	rows, err := s.db.Query(ctx,
		`SELECT b.id, b.code, b.name_vi, b.description_vi, b.icon_url,
		        b.criteria_type, b.criteria_value, b.criteria_ref,
		        ub.earned_at
		 FROM badges b
		 LEFT JOIN user_badges ub ON b.id = ub.badge_id AND ub.user_id = $1
		 WHERE b.is_active = TRUE
		 ORDER BY ub.earned_at DESC NULLS LAST, b.criteria_value ASC`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("list badges: %w", err)
	}
	defer rows.Close()

	earned := make([]map[string]any, 0)
	locked := make([]map[string]any, 0)
	for rows.Next() {
		var id, code, nameVI, descVI, iconURL, criteriaType, criteriaRef string
		var criteriaValue int
		var earnedAt *time.Time
		if err := rows.Scan(&id, &code, &nameVI, &descVI, &iconURL,
			&criteriaType, &criteriaValue, &criteriaRef, &earnedAt); err != nil {
			return nil, fmt.Errorf("scan badge: %w", err)
		}
		b := map[string]any{
			"id":             id,
			"code":           code,
			"name_vi":        nameVI,
			"description_vi": descVI,
			"icon_url":       iconURL,
			"criteria_type":  criteriaType,
			"criteria_value": criteriaValue,
		}
		if earnedAt != nil {
			b["earned_at"] = earnedAt
			earned = append(earned, b)
		} else {
			locked = append(locked, b)
		}
	}

	return map[string]any{"earned": earned, "locked": locked}, nil
}

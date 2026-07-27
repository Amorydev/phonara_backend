package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dailyGoalXP is awarded once the day the minute goal is first reached.
const dailyGoalXP = 50

// DailyMission is the daily practice-time goal shown on Home
// ("Xuất sắc! 15/15 phút"). Time is accumulated from client heartbeats.
type DailyMission struct {
	Date          string `json:"date"`
	GoalMinutes   int    `json:"goal_minutes"`
	MinutesDone   int    `json:"minutes_done"` // capped at goal for "X/X" display
	SecondsDone   int    `json:"seconds_done"` // raw, may exceed goal
	Percent       int    `json:"percent"`      // 0..100 (capped)
	Completed     bool   `json:"completed"`
	Status        string `json:"status"`    // not_started | in_progress | completed
	XPEarned      int    `json:"xp_earned"` // XP earned today from the mission
	JustCompleted bool   `json:"just_completed,omitempty"`
}

// DailyMissionService tracks the daily practice-minutes mission.
type DailyMissionService struct {
	db     *pgxpool.Pool
	streak *StreakService
}

// NewDailyMissionService creates a DailyMissionService. The streak helper only
// touches the DB (no Redis), so a nil Redis client is fine here.
func NewDailyMissionService(db *pgxpool.Pool) *DailyMissionService {
	return &DailyMissionService{db: db, streak: NewStreakService(db, nil)}
}

// Get returns today's mission status (read-only) for the Home widget.
func (s *DailyMissionService) Get(ctx context.Context, userID uuid.UUID) (*DailyMission, error) {
	today, goalMinutes, err := s.todayAndGoal(ctx, userID)
	if err != nil {
		return nil, err
	}

	var secondsDone, xpEarned int
	err = s.db.QueryRow(ctx,
		`SELECT COALESCE(seconds_practiced, 0), COALESCE(xp_earned, 0)
		   FROM daily_progress WHERE user_id = $1 AND date = $2`,
		userID, today).Scan(&secondsDone, &xpEarned)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get daily progress: %w", err)
	}

	m := buildMission(today, goalMinutes, secondsDone)
	m.XPEarned = xpEarned
	return m, nil
}

// Heartbeat adds activeSeconds of practice time to today's total and returns the
// updated mission. Client sends the DELTA since the last heartbeat (additive).
// The first time the goal is reached in a day it awards XP and performs a streak
// check-in (both idempotent for the rest of the day).
func (s *DailyMissionService) Heartbeat(ctx context.Context, userID uuid.UUID, activeSeconds int) (*DailyMission, error) {
	today, goalMinutes, err := s.todayAndGoal(ctx, userID)
	if err != nil {
		return nil, err
	}
	goalSeconds := goalMinutes * 60

	// Upsert the running total and report whether the goal was already met
	// before this heartbeat, so we award the bonus exactly once.
	var total, xpEarned int
	var goalMet, wasMet bool
	err = s.db.QueryRow(ctx,
		`WITH prev AS (
		   SELECT goal_met FROM daily_progress WHERE user_id = $1 AND date = $2
		 ),
		 upsert AS (
		   INSERT INTO daily_progress (user_id, date, seconds_practiced, goal_met)
		   VALUES ($1, $2, $3::int, $3::int >= $4::int)
		   ON CONFLICT (user_id, date) DO UPDATE
		     SET seconds_practiced = daily_progress.seconds_practiced + EXCLUDED.seconds_practiced,
		         goal_met = (daily_progress.seconds_practiced + EXCLUDED.seconds_practiced) >= $4::int
		   RETURNING seconds_practiced, goal_met, xp_earned
		 )
		 SELECT u.seconds_practiced, u.goal_met, u.xp_earned,
		        COALESCE((SELECT goal_met FROM prev), FALSE)
		   FROM upsert u`,
		userID, today, activeSeconds, goalSeconds).Scan(&total, &goalMet, &xpEarned, &wasMet)
	if err != nil {
		return nil, fmt.Errorf("upsert daily progress: %w", err)
	}

	m := buildMission(today, goalMinutes, total)
	m.XPEarned = xpEarned

	// Goal reached for the first time today → award XP + streak check-in.
	if goalMet && !wasMet {
		if err := s.db.QueryRow(ctx,
			`UPDATE daily_progress SET xp_earned = xp_earned + $3
			   WHERE user_id = $1 AND date = $2
			 RETURNING xp_earned`,
			userID, today, dailyGoalXP).Scan(&m.XPEarned); err != nil {
			return nil, fmt.Errorf("award daily xp: %w", err)
		}
		if _, err := s.streak.CheckIn(ctx, userID); err != nil {
			// Non-fatal: the mission is still complete even if streak update fails.
			slog.WarnContext(ctx, "daily mission: streak check-in failed", "err", err)
		}
		m.JustCompleted = true
	}

	return m, nil
}

// todayAndGoal resolves the user's local date (timezone-aware) and minute goal
// in a single query.
func (s *DailyMissionService) todayAndGoal(ctx context.Context, userID uuid.UUID) (string, int, error) {
	var d time.Time
	var goal int
	err := s.db.QueryRow(ctx,
		`SELECT (now() AT TIME ZONE COALESCE(NULLIF(timezone, ''), 'UTC'))::date,
		        daily_goal_minutes
		   FROM users WHERE id = $1`,
		userID).Scan(&d, &goal)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Now().UTC().Format("2006-01-02"), 15, nil
		}
		return "", 0, fmt.Errorf("resolve user goal: %w", err)
	}
	return d.Format("2006-01-02"), goal, nil
}

// buildMission derives the display fields from raw seconds and the goal.
func buildMission(date string, goalMinutes, secondsDone int) *DailyMission {
	goalSeconds := goalMinutes * 60
	completed := goalSeconds > 0 && secondsDone >= goalSeconds

	minutesDone := secondsDone / 60
	if completed {
		minutesDone = goalMinutes // cap so the card reads "15/15"
	}

	percent := 0
	if goalSeconds > 0 {
		percent = secondsDone * 100 / goalSeconds
		if percent > 100 {
			percent = 100
		}
	}

	status := "not_started"
	switch {
	case completed:
		status = "completed"
	case secondsDone > 0:
		status = "in_progress"
	}

	return &DailyMission{
		Date:        date,
		GoalMinutes: goalMinutes,
		MinutesDone: minutesDone,
		SecondsDone: secondsDone,
		Percent:     percent,
		Completed:   completed,
		Status:      status,
	}
}

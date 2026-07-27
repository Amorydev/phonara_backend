package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Home is the aggregated payload for the Home screen, fetched in one call.
// Sections degrade independently: if one fails it is left null and the rest
// are still returned, so a single slow/failing query never blanks the screen.
type Home struct {
	Header        *HomeHeader            `json:"header"`
	DailyMission  *DailyMission          `json:"daily_mission"`
	Challenge     *DailyChallengeSummary `json:"challenge"`
	PracticeModes []*PracticeModeCard    `json:"practice_modes"`
}

// HomeHeader is the greeting + streak strip at the top of Home.
type HomeHeader struct {
	DisplayName    *string `json:"display_name,omitempty"`
	AvatarURL      *string `json:"avatar_url,omitempty"`
	CurrentStreak  int     `json:"current_streak"`
	LongestStreak  int     `json:"longest_streak"`
	LastActiveDate *string `json:"last_active_date,omitempty"`
}

// PracticeModeCard is one navigation card in the "chế độ luyện tập" section.
type PracticeModeCard struct {
	Key       string  `json:"key"`
	Title     string  `json:"title"`
	Subtitle  *string `json:"subtitle,omitempty"`
	Icon      *string `json:"icon,omitempty"`
	Route     string  `json:"route"`
	IsPremium bool    `json:"is_premium"`
	Order     int     `json:"order"`
}

// HomeService composes the Home screen from the underlying feature services.
type HomeService struct {
	db      *pgxpool.Pool
	daily   *DailyService
	mission *DailyMissionService
}

// NewHomeService creates a HomeService.
func NewHomeService(db *pgxpool.Pool) *HomeService {
	return &HomeService{
		db:      db,
		daily:   NewDailyService(db),
		mission: NewDailyMissionService(db),
	}
}

// Get assembles the Home payload. Always returns 200-able data; section errors
// are logged and surfaced as null sections rather than failing the whole call.
func (s *HomeService) Get(ctx context.Context, userID uuid.UUID) (*Home, error) {
	out := &Home{PracticeModes: []*PracticeModeCard{}}

	if h, err := s.header(ctx, userID); err != nil {
		slog.WarnContext(ctx, "home: header section failed", "err", err)
	} else {
		out.Header = h
	}

	if m, err := s.mission.Get(ctx, userID); err != nil {
		slog.WarnContext(ctx, "home: mission section failed", "err", err)
	} else {
		out.DailyMission = m
	}

	if c, err := s.daily.Today(ctx, userID); err != nil {
		slog.WarnContext(ctx, "home: challenge section failed", "err", err)
	} else {
		out.Challenge = c
	}

	if pm, err := s.practiceModes(ctx); err != nil {
		slog.WarnContext(ctx, "home: practice modes section failed", "err", err)
	} else {
		out.PracticeModes = pm
	}

	return out, nil
}

// header loads the user's display name and streak strip.
func (s *HomeService) header(ctx context.Context, userID uuid.UUID) (*HomeHeader, error) {
	h := &HomeHeader{}
	var lastActive *time.Time
	err := s.db.QueryRow(ctx,
		`SELECT u.display_name, u.avatar_url,
		        COALESCE(s.current_streak, 0),
		        COALESCE(s.longest_streak, 0),
		        s.last_active_date
		   FROM users u
		   LEFT JOIN streak_records s ON s.user_id = u.id
		  WHERE u.id = $1`,
		userID).Scan(&h.DisplayName, &h.AvatarURL, &h.CurrentStreak, &h.LongestStreak, &lastActive)
	if err != nil {
		return nil, fmt.Errorf("load home header: %w", err)
	}
	if lastActive != nil {
		d := lastActive.Format("2006-01-02")
		h.LastActiveDate = &d
	}
	return h, nil
}

// practiceModes loads the active practice-mode cards, ordered.
func (s *HomeService) practiceModes(ctx context.Context) ([]*PracticeModeCard, error) {
	rows, err := s.db.Query(ctx,
		`SELECT key, title_vi, subtitle_vi, icon, route, is_premium, order_index
		   FROM practice_modes
		  WHERE is_active = TRUE
		  ORDER BY order_index`)
	if err != nil {
		return nil, fmt.Errorf("list practice modes: %w", err)
	}
	defer rows.Close()

	cards := make([]*PracticeModeCard, 0)
	for rows.Next() {
		c := &PracticeModeCard{}
		if err := rows.Scan(&c.Key, &c.Title, &c.Subtitle, &c.Icon, &c.Route, &c.IsPremium, &c.Order); err != nil {
			return nil, fmt.Errorf("scan practice mode: %w", err)
		}
		cards = append(cards, c)
	}
	return cards, rows.Err()
}

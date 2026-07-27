package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phonara/backend/internal/pkg/apperrors"
)

// DailyChallengeSummary is the lightweight card returned by GET /daily/today.
// It carries everything the Home screen needs (title, banner, item count,
// completion status) WITHOUT the heavy resolved item list — the client then
// navigates into the challenge screen carrying only challenge_id and fetches
// the full content from GET /daily/challenges/{id}.
type DailyChallengeSummary struct {
	ChallengeID string  `json:"challenge_id,omitempty"`
	Date        string  `json:"date"`
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Category    *string `json:"category,omitempty"`
	BannerURL   *string `json:"banner_url,omitempty"`
	Moderated   bool    `json:"moderated"`
	UserStatus  *string `json:"user_status,omitempty"` // completed | in_progress | missed | null
	Available   bool    `json:"available"`
	Message     string  `json:"message,omitempty"`
	ItemCount   int     `json:"item_count"`
}

// DailyChallenge is the fully-resolved bundle returned by
// GET /daily/challenges/{id} — summary fields plus every item with its content
// embedded, so the challenge screen renders from one response.
type DailyChallenge struct {
	DailyChallengeSummary
	Items []*DailyChallengeItem `json:"items"`
}

// DailyChallengeItem is one entry in the challenge set. Exactly one of
// ContentItem or Passage is populated, indicated by Kind.
type DailyChallengeItem struct {
	Order       int           `json:"order"`
	Kind        string        `json:"kind"` // word | sentence | passage
	ContentItem *ContentItem  `json:"content_item,omitempty"`
	Passage     *DailyPassage `json:"passage,omitempty"`
}

// DailyPassage is a shadowing passage with its sentences embedded so the
// shadowing screen has everything it needs in one call.
type DailyPassage struct {
	ID            string                 `json:"id"`
	Title         string                 `json:"title"`
	Source        string                 `json:"source"`
	Topic         *string                `json:"topic,omitempty"`
	Difficulty    int                    `json:"difficulty"`
	SentenceCount int                    `json:"sentence_count"`
	Sentences     []*PassageSentenceItem `json:"sentences"`
}

// DailyHistoryItem is one entry in the user's daily challenge history.
// ChallengeID lets the client deep-link into GET /daily/challenges/{id}.
type DailyHistoryItem struct {
	ChallengeID string     `json:"challenge_id"`
	Date        string     `json:"date"`
	Title       *string    `json:"title,omitempty"`
	Category    *string    `json:"category,omitempty"`
	Status      string     `json:"status"`
	Score       *float64   `json:"score,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// DailyService handles daily challenges.
type DailyService struct {
	db *pgxpool.Pool
}

// NewDailyService creates a DailyService.
func NewDailyService(db *pgxpool.Pool) *DailyService {
	return &DailyService{db: db}
}

// Today returns the lightweight summary of today's challenge for the Home card.
// "Today" is computed in the user's own timezone (consistent with streak
// check-in), so the date rolls over at local midnight rather than UTC.
func (s *DailyService) Today(ctx context.Context, userID uuid.UUID) (*DailyChallengeSummary, error) {
	today, err := s.localToday(ctx, userID)
	if err != nil {
		return nil, err
	}

	out := &DailyChallengeSummary{Date: today}

	err = s.db.QueryRow(ctx,
		`SELECT dc.id, dc.title, dc.description, dc.category, dc.banner_url, dc.moderated,
		        (SELECT count(*) FROM daily_challenge_items i WHERE i.challenge_id = dc.id)
		   FROM daily_challenges dc WHERE dc.date = $1`,
		today,
	).Scan(&out.ChallengeID, &out.Title, &out.Description, &out.Category,
		&out.BannerURL, &out.Moderated, &out.ItemCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			out.Available = false
			out.Message = "No challenge available today"
			return out, nil
		}
		return nil, fmt.Errorf("get daily challenge: %w", err)
	}
	out.Available = true

	// User's completion status for this challenge (null if untouched).
	_ = s.db.QueryRow(ctx,
		`SELECT status FROM daily_challenge_completions WHERE user_id = $1 AND challenge_id = $2`,
		userID, out.ChallengeID,
	).Scan(&out.UserStatus)

	return out, nil
}

// GetChallenge returns a challenge fully resolved by id, for the challenge
// screen. Works for any challenge (today or a past one deep-linked from
// history). Returns apperrors.ErrNotFound when the id does not exist.
func (s *DailyService) GetChallenge(ctx context.Context, userID uuid.UUID, challengeID string) (*DailyChallenge, error) {
	out := &DailyChallenge{Items: []*DailyChallengeItem{}}

	var date time.Time
	err := s.db.QueryRow(ctx,
		`SELECT id, date, title, description, category, banner_url, moderated
		   FROM daily_challenges WHERE id = $1`,
		challengeID,
	).Scan(&out.ChallengeID, &date, &out.Title, &out.Description, &out.Category, &out.BannerURL, &out.Moderated)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("get daily challenge: %w", err)
	}
	out.Date = date.Format("2006-01-02")
	out.Available = true

	_ = s.db.QueryRow(ctx,
		`SELECT status FROM daily_challenge_completions WHERE user_id = $1 AND challenge_id = $2`,
		userID, out.ChallengeID,
	).Scan(&out.UserStatus)

	items, err := s.resolveItems(ctx, out.ChallengeID)
	if err != nil {
		return nil, err
	}
	out.Items = items
	out.ItemCount = len(items)
	return out, nil
}

// resolveItems loads every item of a challenge with its content embedded.
// Uses 3 queries total (content items, passages, passage sentences) — no N+1.
func (s *DailyService) resolveItems(ctx context.Context, challengeID string) ([]*DailyChallengeItem, error) {
	items := make([]*DailyChallengeItem, 0)

	// Word / sentence items.
	rows, err := s.db.Query(ctx,
		`SELECT i.order_index, i.kind,
		        c.id, c.type, c.text, c.ipa, c.audio_url_us, c.audio_url_uk,
		        c.topic, c.difficulty, c.focus_phonemes
		   FROM daily_challenge_items i
		   JOIN content_items c ON i.content_item_id = c.id
		  WHERE i.challenge_id = $1 AND i.content_item_id IS NOT NULL AND c.is_active = TRUE`,
		challengeID)
	if err != nil {
		return nil, fmt.Errorf("resolve content items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var order int
		var kind string
		ci := &ContentItem{}
		if err := rows.Scan(&order, &kind, &ci.ID, &ci.Type, &ci.Text, &ci.IPA,
			&ci.AudioURLUS, &ci.AudioURLUK, &ci.Topic, &ci.Difficulty, &ci.FocusPhonemes); err != nil {
			return nil, fmt.Errorf("scan content item: %w", err)
		}
		items = append(items, &DailyChallengeItem{Order: order, Kind: kind, ContentItem: ci})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate content items: %w", err)
	}

	// Passage items.
	prows, err := s.db.Query(ctx,
		`SELECT i.order_index, p.id, p.title, p.source, p.topic, p.difficulty, p.sentence_count
		   FROM daily_challenge_items i
		   JOIN shadowing_passages p ON i.passage_id = p.id
		  WHERE i.challenge_id = $1 AND i.passage_id IS NOT NULL AND p.is_active = TRUE
		  ORDER BY i.order_index`,
		challengeID)
	if err != nil {
		return nil, fmt.Errorf("resolve passages: %w", err)
	}
	defer prows.Close()
	passageByID := make(map[string]*DailyPassage)
	var passageIDs []string
	for prows.Next() {
		var order int
		p := &DailyPassage{Sentences: []*PassageSentenceItem{}}
		if err := prows.Scan(&order, &p.ID, &p.Title, &p.Source, &p.Topic, &p.Difficulty, &p.SentenceCount); err != nil {
			return nil, fmt.Errorf("scan passage: %w", err)
		}
		items = append(items, &DailyChallengeItem{Order: order, Kind: "passage", Passage: p})
		passageByID[p.ID] = p
		passageIDs = append(passageIDs, p.ID)
	}
	if err := prows.Err(); err != nil {
		return nil, fmt.Errorf("iterate passages: %w", err)
	}

	// Embed sentences for all passages in one query.
	if len(passageIDs) > 0 {
		srows, err := s.db.Query(ctx,
			`SELECT passage_id, id, order_index, text, ipa, native_audio_url
			   FROM passage_sentences
			  WHERE passage_id = ANY($1)
			  ORDER BY passage_id, order_index`,
			passageIDs)
		if err != nil {
			return nil, fmt.Errorf("resolve passage sentences: %w", err)
		}
		defer srows.Close()
		for srows.Next() {
			var passageID string
			sn := &PassageSentenceItem{}
			if err := srows.Scan(&passageID, &sn.ID, &sn.OrderIndex, &sn.Text, &sn.IPA, &sn.NativeAudioURL); err != nil {
				return nil, fmt.Errorf("scan passage sentence: %w", err)
			}
			if p, ok := passageByID[passageID]; ok {
				p.Sentences = append(p.Sentences, sn)
			}
		}
		if err := srows.Err(); err != nil {
			return nil, fmt.Errorf("iterate passage sentences: %w", err)
		}
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Order < items[j].Order })
	return items, nil
}

// History returns the user's daily challenge history (most recent 30).
func (s *DailyService) History(ctx context.Context, userID uuid.UUID) ([]*DailyHistoryItem, error) {
	rows, err := s.db.Query(ctx,
		`SELECT dc.id, dc.date, dc.title, dc.category, dcc.status, dcc.score, dcc.completed_at
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

	items := make([]*DailyHistoryItem, 0)
	for rows.Next() {
		var date time.Time
		it := &DailyHistoryItem{}
		if err := rows.Scan(&it.ChallengeID, &date, &it.Title, &it.Category, &it.Status, &it.Score, &it.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan daily history: %w", err)
		}
		it.Date = date.Format("2006-01-02")
		items = append(items, it)
	}
	return items, rows.Err()
}

// localToday returns today's date string in the user's timezone (fallback UTC).
func (s *DailyService) localToday(ctx context.Context, userID uuid.UUID) (string, error) {
	var d time.Time
	err := s.db.QueryRow(ctx,
		`SELECT (now() AT TIME ZONE COALESCE(NULLIF(timezone, ''), 'UTC'))::date
		   FROM users WHERE id = $1`,
		userID).Scan(&d)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Now().UTC().Format("2006-01-02"), nil
		}
		return "", fmt.Errorf("resolve user timezone: %w", err)
	}
	return d.Format("2006-01-02"), nil
}

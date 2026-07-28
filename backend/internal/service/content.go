package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phonara/backend/internal/pkg/apperrors"
)

// ContentFilter holds query parameters for content listings.
type ContentFilter struct {
	Topic      string
	Goal       string
	Phoneme    string
	L1Tag      string
	Category   string
	Source     string
	Difficulty string
}

// ContentItem is the DTO for a word or sentence.
type ContentItem struct {
	ID            string   `json:"id"`
	Type          string   `json:"type"`
	Text          string   `json:"text"`
	IPA           *string  `json:"ipa,omitempty"`
	AudioURLUS    *string  `json:"audio_url_us,omitempty"`
	AudioURLUK    *string  `json:"audio_url_uk,omitempty"`
	Topic         *string  `json:"topic,omitempty"`
	Difficulty    int      `json:"difficulty"`
	FocusPhonemes []string `json:"focus_phonemes,omitempty"`
}

// MinimalPairItem is the DTO for a minimal pair.
type MinimalPairItem struct {
	ID        string  `json:"id"`
	WordA     string  `json:"word_a"`
	WordB     string  `json:"word_b"`
	PhonemeA  string  `json:"phoneme_a"`
	PhonemeB  string  `json:"phoneme_b"`
	AudioAUS  *string `json:"audio_a_us,omitempty"`
	AudioBUS  *string `json:"audio_b_us,omitempty"`
	ExplainVI *string `json:"explain_vi,omitempty"`
	Category  *string `json:"category,omitempty"`
}

// PassageItem is the DTO for a shadowing passage.
type PassageItem struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Source        string  `json:"source"`
	Topic         *string `json:"topic,omitempty"`
	Difficulty    int     `json:"difficulty"`
	SentenceCount int     `json:"sentence_count"`
}

// PassageSentenceItem is a sentence within a passage.
type PassageSentenceItem struct {
	ID             string  `json:"id"`
	OrderIndex     int     `json:"order_index"`
	Text           string  `json:"text"`
	IPA            *string `json:"ipa,omitempty"`
	NativeAudioURL *string `json:"native_audio_url,omitempty"`
}

// FixGuideItem is the DTO for a fix guide.
type FixGuideItem struct {
	ID           string  `json:"id"`
	TonguePosVI  *string `json:"tongue_position_vi,omitempty"`
	MediaURL     *string `json:"media_url,omitempty"`
	Examples     any     `json:"examples"`
	IsSimplified bool    `json:"is_simplified"`
}

// ContentService handles content retrieval.
type ContentService struct {
	db *pgxpool.Pool
}

// NewContentService creates a ContentService.
func NewContentService(db *pgxpool.Pool) *ContentService {
	return &ContentService{db: db}
}

// ListWords retrieves active words matching the filter.
func (s *ContentService) ListWords(ctx context.Context, f ContentFilter) ([]*ContentItem, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, type, text, ipa, audio_url_us, audio_url_uk, topic, difficulty, focus_phonemes
		 FROM content_items
		 WHERE type = 'word' AND is_active = TRUE
		   AND ($1 = '' OR topic = $1)
		   AND ($2 = '' OR $2 = ANY(target_goal))
		   AND ($3 = '' OR $3 = ANY(focus_phonemes))
		 ORDER BY difficulty, text
		 LIMIT 50`,
		f.Topic, f.Goal, f.Phoneme)
	if err != nil {
		return nil, fmt.Errorf("list words: %w", err)
	}
	defer rows.Close()
	return s.scanContentItems(rows)
}

// ListSentences retrieves active sentences matching the filter.
func (s *ContentService) ListSentences(ctx context.Context, f ContentFilter) ([]*ContentItem, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, type, text, ipa, audio_url_us, audio_url_uk, topic, difficulty, focus_phonemes
		 FROM content_items
		 WHERE type = 'sentence' AND is_active = TRUE
		   AND ($1 = '' OR topic = $1)
		   AND ($2 = '' OR $2 = ANY(target_goal))
		 ORDER BY difficulty
		 LIMIT 50`,
		f.Topic, f.Goal)
	if err != nil {
		return nil, fmt.Errorf("list sentences: %w", err)
	}
	defer rows.Close()
	return s.scanContentItems(rows)
}

// ListMinimalPairs retrieves active minimal pairs.
func (s *ContentService) ListMinimalPairs(ctx context.Context, f ContentFilter) ([]*MinimalPairItem, error) {
	rows, err := s.db.Query(ctx,
		`SELECT mp.id, mp.word_a, mp.word_b, mp.phoneme_a, mp.phoneme_b,
		        mp.audio_a_us, mp.audio_b_us, mp.explain_vi, mp.category
		 FROM minimal_pairs mp
		 LEFT JOIN l1_error_tags l ON mp.l1_tag_id = l.id
		 WHERE mp.is_active = TRUE
		   AND ($1 = '' OR l.code = $1)
		   AND ($2 = '' OR mp.category = $2)
		 ORDER BY mp.difficulty
		 LIMIT 100`,
		f.L1Tag, f.Category)
	if err != nil {
		return nil, fmt.Errorf("list minimal pairs: %w", err)
	}
	defer rows.Close()

	var items []*MinimalPairItem
	for rows.Next() {
		item := &MinimalPairItem{}
		if err := rows.Scan(&item.ID, &item.WordA, &item.WordB, &item.PhonemeA, &item.PhonemeB,
			&item.AudioAUS, &item.AudioBUS, &item.ExplainVI, &item.Category); err != nil {
			return nil, fmt.Errorf("scan minimal pair: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListPassages retrieves active shadowing passages.
func (s *ContentService) ListPassages(ctx context.Context, f ContentFilter) ([]*PassageItem, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, title, source, topic, difficulty, sentence_count
		 FROM shadowing_passages
		 WHERE is_active = TRUE
		   AND ($1 = '' OR source = $1)
		 ORDER BY difficulty
		 LIMIT 50`,
		f.Source)
	if err != nil {
		return nil, fmt.Errorf("list passages: %w", err)
	}
	defer rows.Close()

	var items []*PassageItem
	for rows.Next() {
		item := &PassageItem{}
		if err := rows.Scan(&item.ID, &item.Title, &item.Source, &item.Topic,
			&item.Difficulty, &item.SentenceCount); err != nil {
			return nil, fmt.Errorf("scan passage: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListPassageSentences retrieves sentences for a passage.
func (s *ContentService) ListPassageSentences(ctx context.Context, passageID string) ([]*PassageSentenceItem, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, order_index, text, ipa, native_audio_url
		 FROM passage_sentences
		 WHERE passage_id = $1
		 ORDER BY order_index`,
		passageID)
	if err != nil {
		return nil, fmt.Errorf("list passage sentences: %w", err)
	}
	defer rows.Close()

	var items []*PassageSentenceItem
	for rows.Next() {
		item := &PassageSentenceItem{}
		if err := rows.Scan(&item.ID, &item.OrderIndex, &item.Text, &item.IPA, &item.NativeAudioURL); err != nil {
			return nil, fmt.Errorf("scan sentence: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetFixGuide retrieves a fix guide for the given phoneme or L1 tag.
// Falls back to simplified guide if rich content unavailable (BR-L1-02).
func (s *ContentService) GetFixGuide(ctx context.Context, f ContentFilter) (*FixGuideItem, error) {
	item := &FixGuideItem{}
	err := s.db.QueryRow(ctx,
		`SELECT id, tongue_position_vi, media_url, examples
		 FROM fix_guides
		 WHERE is_active = TRUE
		   AND ($1 = '' OR phoneme = $1)
		 LIMIT 1`,
		f.Phoneme).Scan(&item.ID, &item.TonguePosVI, &item.MediaURL, &item.Examples)
	if err != nil {
		// Fallback: return simplified guide indicator
		if f.Phoneme != "" {
			return &FixGuideItem{
				IsSimplified: true,
				Examples:     []string{},
			}, nil
		}
		return nil, apperrors.ErrNotFound
	}
	return item, nil
}

func (s *ContentService) scanContentItems(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]*ContentItem, error) {
	var items []*ContentItem
	for rows.Next() {
		item := &ContentItem{}
		if err := rows.Scan(&item.ID, &item.Type, &item.Text, &item.IPA,
			&item.AudioURLUS, &item.AudioURLUK, &item.Topic, &item.Difficulty, &item.FocusPhonemes); err != nil {
			return nil, fmt.Errorf("scan content item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

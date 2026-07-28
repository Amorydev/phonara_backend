package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phonara/backend/internal/domain"
	"github.com/phonara/backend/internal/pkg/apperrors"
)

// ShadowingProgressDTO is the progress state for a user on a passage.
type ShadowingProgressDTO struct {
	PassageID            string   `json:"passage_id"`
	CurrentSentenceIndex int      `json:"current_sentence_index"`
	SentenceStatus       any      `json:"sentence_status"`
	PassageAvgScore      *float64 `json:"passage_avg_score,omitempty"`
	Completed            bool     `json:"completed"`
}

// ShadowingSentenceInput holds the input for submitting a sentence result.
type ShadowingSentenceInput struct {
	UserID       uuid.UUID
	PassageID    string
	OrderIndex   int
	ScoringLevel string
	PARaw        domain.PARawPayload
	AudioRef     string
}

// ShadowingSentenceResult is the result of submitting a sentence.
type ShadowingSentenceResult struct {
	Score      *float64 `json:"score"`
	Passed     bool     `json:"passed"`
	CanSkip    bool     `json:"can_skip"`
	NextAction string   `json:"next_action"` // advance | retry | skip_available
}

// ShadowingService handles shadowing passage progress.
type ShadowingService struct {
	db *pgxpool.Pool
}

// NewShadowingService creates a ShadowingService.
func NewShadowingService(db *pgxpool.Pool) *ShadowingService {
	return &ShadowingService{db: db}
}

// GetProgress retrieves shadowing progress for a user/passage.
func (s *ShadowingService) GetProgress(ctx context.Context, userID uuid.UUID, passageID string) (*ShadowingProgressDTO, error) {
	dto := &ShadowingProgressDTO{PassageID: passageID}
	err := s.db.QueryRow(ctx,
		`SELECT current_sentence_index, sentence_status, passage_avg_score, completed
		 FROM shadowing_progress
		 WHERE user_id = $1 AND passage_id = $2`,
		userID, passageID).Scan(
		&dto.CurrentSentenceIndex, &dto.SentenceStatus,
		&dto.PassageAvgScore, &dto.Completed,
	)
	if err != nil {
		// Not started — return initial state
		return &ShadowingProgressDTO{PassageID: passageID, SentenceStatus: []any{}}, nil
	}
	return dto, nil
}

// SubmitSentenceResult processes a shadowing sentence result.
// Applies 80% threshold (BR-SHAD-02) and manages skip logic (BR-SHAD-04).
func (s *ShadowingService) SubmitSentenceResult(ctx context.Context, in ShadowingSentenceInput) (*ShadowingSentenceResult, error) {
	// Compute score from PA raw (reuse level scoring logic)
	// nil enqueue: chỉ mượn applyLevelScoring — hàm thuần, không đẩy task nào.
	scoreSvc := NewSessionService(s.db, nil)
	acc, _, _, _ := scoreSvc.applyLevelScoring(in.PARaw, in.ScoringLevel)

	var score float64
	if acc != nil {
		score = *acc
	}

	passed := score >= 80.0 // BR-SHAD-02

	// Ensure progress row exists
	_, err := s.db.Exec(ctx,
		`INSERT INTO shadowing_progress (user_id, passage_id)
		 VALUES ($1, $2)
		 ON CONFLICT (user_id, passage_id) DO NOTHING`,
		in.UserID, in.PassageID)
	if err != nil {
		return nil, fmt.Errorf("upsert shadowing progress: %w", err)
	}

	// Get attempt count for this sentence
	var attemptsJSON []byte
	s.db.QueryRow(ctx,
		`SELECT attempts_per_sentence FROM shadowing_progress WHERE user_id = $1 AND passage_id = $2`,
		in.UserID, in.PassageID).Scan(&attemptsJSON)

	canSkip := false
	// TODO: parse attemptsJSON to count attempts per sentence index; enable skip after 3 attempts

	nextAction := "retry"
	if passed {
		nextAction = "advance"
		// Advance sentence index
		_, err = s.db.Exec(ctx,
			`UPDATE shadowing_progress
			 SET current_sentence_index = current_sentence_index + 1
			 WHERE user_id = $1 AND passage_id = $2 AND current_sentence_index = $3`,
			in.UserID, in.PassageID, in.OrderIndex)
		if err != nil {
			return nil, fmt.Errorf("advance sentence: %w", err)
		}
	} else if canSkip {
		nextAction = "skip_available"
	}

	return &ShadowingSentenceResult{
		Score:      acc,
		Passed:     passed,
		CanSkip:    canSkip,
		NextAction: nextAction,
	}, nil
}

// Complete marks a shadowing passage as completed and returns summary.
func (s *ShadowingService) Complete(ctx context.Context, userID uuid.UUID, passageID string) (map[string]any, error) {
	_, err := s.db.Exec(ctx,
		`UPDATE shadowing_progress SET completed = TRUE
		 WHERE user_id = $1 AND passage_id = $2`,
		userID, passageID)
	if err != nil {
		return nil, fmt.Errorf("complete passage: %w", err)
	}

	progress, err := s.GetProgress(ctx, userID, passageID)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"passage_id": passageID,
		"completed":  true,
		"avg_score":  progress.PassageAvgScore,
	}, nil
}

// MinimalPairService handles minimal pair drills.
type MinimalPairService struct {
	db  *pgxpool.Pool
	rdb interface{} // goredis.Client — optional
}

// NewMinimalPairService creates a MinimalPairService.
func NewMinimalPairService(db *pgxpool.Pool, rdb interface{}) *MinimalPairService {
	return &MinimalPairService{db: db, rdb: rdb}
}

// StartListenDrill creates a new listen drill with random minimal pairs.
func (s *MinimalPairService) StartListenDrill(ctx context.Context, userID uuid.UUID, count int) (map[string]any, error) {
	drillID := uuid.New()
	_, err := s.db.Exec(ctx,
		`INSERT INTO mp_listen_drills (id, user_id, total_items, hearts_left)
		 VALUES ($1, $2, $3, 3)`,
		drillID, userID, count)
	if err != nil {
		return nil, fmt.Errorf("create listen drill: %w", err)
	}

	// Pick random pairs
	rows, err := s.db.Query(ctx,
		`SELECT id, word_a, word_b, audio_a_us, audio_b_us
		 FROM minimal_pairs
		 WHERE is_active = TRUE
		 ORDER BY RANDOM()
		 LIMIT $1`,
		count)
	if err != nil {
		return nil, fmt.Errorf("pick minimal pairs: %w", err)
	}
	defer rows.Close()

	pairs := make([]map[string]any, 0, count)
	for rows.Next() {
		var id, wordA, wordB string
		var audioA, audioB *string
		if err := rows.Scan(&id, &wordA, &wordB, &audioA, &audioB); err != nil {
			return nil, fmt.Errorf("scan pair: %w", err)
		}
		pairs = append(pairs, map[string]any{
			"pair_id": id, "word_a": wordA, "word_b": wordB,
			"audio_a": audioA, "audio_b": audioB,
		})
	}

	return map[string]any{
		"drill_id":    drillID,
		"total_items": count,
		"hearts_left": 3,
		"pairs":       pairs,
	}, nil
}

// SubmitAnswer processes a listen drill answer.
func (s *MinimalPairService) SubmitAnswer(ctx context.Context, userID uuid.UUID, drillID, pairID, chosenWord string) (map[string]any, error) {
	// Get the pair to determine the correct answer (randomly assigned played_word)
	var wordA string
	err := s.db.QueryRow(ctx,
		`SELECT word_a FROM minimal_pairs WHERE id = $1`, pairID).Scan(&wordA)
	if err != nil {
		return nil, apperrors.ErrNotFound
	}

	// For simplicity, the "played" word is word_a; real implementation randomizes
	playedWord := wordA
	isCorrect := chosenWord == playedWord

	// Record answer
	_, err = s.db.Exec(ctx,
		`INSERT INTO mp_listen_answers (drill_id, minimal_pair_id, played_word, chosen_word, is_correct)
		 VALUES ($1, $2, $3, $4, $5)`,
		drillID, pairID, playedWord, chosenWord, isCorrect)
	if err != nil {
		return nil, fmt.Errorf("record answer: %w", err)
	}

	if !isCorrect {
		// Decrement hearts
		s.db.Exec(ctx,
			`UPDATE mp_listen_drills SET hearts_left = GREATEST(hearts_left - 1, 0) WHERE id = $1 AND user_id = $2`,
			drillID, userID)
	}

	return map[string]any{
		"is_correct":  isCorrect,
		"played_word": playedWord,
	}, nil
}

// GetDrillStatus retrieves the current state of a listen drill.
func (s *MinimalPairService) GetDrillStatus(ctx context.Context, userID uuid.UUID, drillID string) (map[string]any, error) {
	var total, correct, hearts int
	var status string
	err := s.db.QueryRow(ctx,
		`SELECT total_items, correct_count, hearts_left, status
		 FROM mp_listen_drills WHERE id = $1 AND user_id = $2`,
		drillID, userID).Scan(&total, &correct, &hearts, &status)
	if err != nil {
		return nil, apperrors.ErrNotFound
	}
	return map[string]any{
		"drill_id":      drillID,
		"total_items":   total,
		"correct_count": correct,
		"hearts_left":   hearts,
		"status":        status,
	}, nil
}

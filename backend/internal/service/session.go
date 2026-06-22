package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/phonara/backend/internal/config"
	"github.com/phonara/backend/internal/domain"
	"github.com/phonara/backend/internal/pkg/apperrors"
)

// NOTE: handler is imported for PARawPayload; in a real refactor this type
// would live in internal/domain. Kept here for brevity.

// CreateSessionInput holds parameters for creating a new practice session.
type CreateSessionInput struct {
	UserID       uuid.UUID
	Mode         string
	Source       string
	ScoringLevel string
}

// SessionDTO is the public session representation.
type SessionDTO struct {
	ID           uuid.UUID  `json:"id"`
	Mode         string     `json:"mode"`
	ScoringLevel string     `json:"scoring_level"`
	Source       *string    `json:"source,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	SummaryScore *float64   `json:"summary_score,omitempty"`
}

// IngestResultInput holds the raw PA data and metadata for ingestion.
type IngestResultInput struct {
	UserID         uuid.UUID
	SessionID      string
	ContentItemID  string
	MinimalPairID  string
	ScoringLevel   string
	IdempotencyKey string
	PARaw          domain.PARawPayload
	AudioRef       string
}

// IngestResultOutput is the result of ingesting a practice item result.
type IngestResultOutput struct {
	ItemResultID uuid.UUID `json:"item_result_id"`
	Accuracy     *float64  `json:"accuracy"`
	Completeness *float64  `json:"completeness"`
	Fluency      *float64  `json:"fluency"`
	Prosody      *float64  `json:"prosody"`
	TrustFlag    string    `json:"trust_flag"`
}

// SessionService handles practice session business logic.
type SessionService struct {
	db  *pgxpool.Pool
	rdb *goredis.Client
	cfg *config.Config
}

// NewSessionService creates a SessionService.
func NewSessionService(db *pgxpool.Pool, rdb *goredis.Client, cfg *config.Config) *SessionService {
	return &SessionService{db: db, rdb: rdb, cfg: cfg}
}

// Create opens a new practice session after gating checks.
func (s *SessionService) Create(ctx context.Context, in CreateSessionInput) (*SessionDTO, error) {
	// Default scoring_level to user's default if not provided
	scoringLevel := in.ScoringLevel
	if scoringLevel == "" {
		if err := s.db.QueryRow(ctx,
			`SELECT default_scoring_level FROM users WHERE id = $1`,
			in.UserID).Scan(&scoringLevel); err != nil {
			scoringLevel = "medium"
		}
	}

	sessionID := uuid.New()
	var source *string
	if in.Source != "" {
		source = &in.Source
	}

	_, err := s.db.Exec(ctx,
		`INSERT INTO practice_sessions (id, user_id, mode, scoring_level, source, started_at)
		 VALUES ($1, $2, $3, $4, $5, now())`,
		sessionID, in.UserID, in.Mode, scoringLevel, source)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	return &SessionDTO{
		ID:           sessionID,
		Mode:         in.Mode,
		ScoringLevel: scoringLevel,
		Source:       source,
		StartedAt:    time.Now(),
	}, nil
}

// Get retrieves a session by ID (verifying ownership).
func (s *SessionService) Get(ctx context.Context, userID uuid.UUID, sessionID string) (*SessionDTO, error) {
	dto := &SessionDTO{}
	err := s.db.QueryRow(ctx,
		`SELECT id, mode, scoring_level, source, started_at, ended_at, summary_score
		 FROM practice_sessions WHERE id = $1 AND user_id = $2`,
		sessionID, userID).Scan(
		&dto.ID, &dto.Mode, &dto.ScoringLevel, &dto.Source,
		&dto.StartedAt, &dto.EndedAt, &dto.SummaryScore,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return dto, nil
}

// End closes a session and computes summary score.
func (s *SessionService) End(ctx context.Context, userID uuid.UUID, sessionID string) (*SessionDTO, error) {
	// Compute average accuracy from results
	var avgAccuracy *float64
	_ = s.db.QueryRow(ctx,
		`SELECT AVG(accuracy) FROM practice_item_results
		 WHERE session_id = $1`,
		sessionID).Scan(&avgAccuracy)

	_, err := s.db.Exec(ctx,
		`UPDATE practice_sessions SET ended_at = now(), summary_score = $1
		 WHERE id = $2 AND user_id = $3`,
		avgAccuracy, sessionID, userID)
	if err != nil {
		return nil, fmt.Errorf("end session: %w", err)
	}

	return s.Get(ctx, userID, sessionID)
}

// History returns practice session history for a user.
func (s *SessionService) History(ctx context.Context, userID uuid.UUID) ([]*SessionDTO, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, mode, scoring_level, source, started_at, ended_at, summary_score
		 FROM practice_sessions
		 WHERE user_id = $1
		 ORDER BY started_at DESC
		 LIMIT 50`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("query history: %w", err)
	}
	defer rows.Close()

	var sessions []*SessionDTO
	for rows.Next() {
		dto := &SessionDTO{}
		if err := rows.Scan(&dto.ID, &dto.Mode, &dto.ScoringLevel, &dto.Source,
			&dto.StartedAt, &dto.EndedAt, &dto.SummaryScore); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, dto)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return sessions, nil
}

// InProgress returns the most recent in-progress session (not ended).
func (s *SessionService) InProgress(ctx context.Context, userID uuid.UUID) (*SessionDTO, error) {
	dto := &SessionDTO{}
	err := s.db.QueryRow(ctx,
		`SELECT id, mode, scoring_level, source, started_at, ended_at, summary_score
		 FROM practice_sessions
		 WHERE user_id = $1 AND ended_at IS NULL
		 ORDER BY started_at DESC LIMIT 1`,
		userID).Scan(
		&dto.ID, &dto.Mode, &dto.ScoringLevel, &dto.Source,
		&dto.StartedAt, &dto.EndedAt, &dto.SummaryScore,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // no in-progress session is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("get in-progress session: %w", err)
	}
	return dto, nil
}

// IngestResult processes a single PA result from the client.
func (s *SessionService) IngestResult(ctx context.Context, in IngestResultInput) (*IngestResultOutput, error) {
	// Idempotency check (Redis SET NX, 24h TTL)
	idempKey := fmt.Sprintf("idem:result:%s", in.IdempotencyKey)
	set, err := s.rdb.SetNX(ctx, idempKey, "1", 24*time.Hour).Result()
	if err != nil {
		return nil, fmt.Errorf("idempotency check: %w", err)
	}
	if !set {
		// Already processed — return 200 silently (idempotent)
		return &IngestResultOutput{TrustFlag: "ok"}, nil
	}

	// Verify session belongs to user
	var sessionMode string
	err = s.db.QueryRow(ctx,
		`SELECT mode FROM practice_sessions WHERE id = $1 AND user_id = $2`,
		in.SessionID, in.UserID).Scan(&sessionMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find session: %w", err)
	}

	// Recording-fail handling: if PA result indicates failure, don't score
	if s.isRecordingFailed(in.PARaw) {
		// Log event but don't create result
		go s.logAnalyticsEvent(ctx, in.UserID, "recording_failed", map[string]any{
			"session_id": in.SessionID,
		})
		return nil, apperrors.New(422, "recording quality insufficient, please try again", apperrors.ErrUnprocessable)
	}

	// Apply Level scoring (server-side) — §3b.0a
	accuracy, completeness, fluency, prosody := s.applyLevelScoring(in.PARaw, in.ScoringLevel)

	// Sanity-check trust boundary
	trustFlag := s.sanityCheck(accuracy, completeness, in.PARaw)

	// Persist item result
	itemResultID := uuid.New()
	miscueJSON, _ := json.Marshal(s.buildMiscue(in.PARaw))

	var contentItemID, minimalPairID *string
	if in.ContentItemID != "" {
		contentItemID = &in.ContentItemID
	}
	if in.MinimalPairID != "" {
		minimalPairID = &in.MinimalPairID
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin ingest tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx,
		`INSERT INTO practice_item_results
		   (id, session_id, content_item_id, minimal_pair_id, scoring_level,
		    accuracy, fluency, completeness, prosody, miscue, audio_ref, trust_flag)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		itemResultID, in.SessionID, contentItemID, minimalPairID, in.ScoringLevel,
		accuracy, fluency, completeness, prosody, string(miscueJSON),
		nullIfEmpty(in.AudioRef), trustFlag,
	)
	if err != nil {
		return nil, fmt.Errorf("insert item result: %w", err)
	}

	// Persist phoneme scores (always, regardless of Level — BR-LEVEL-05)
	for _, ph := range in.PARaw.Phonemes {
		isOmission := ph.Said == "" || ph.Accuracy < 10
		_, err = tx.Exec(ctx,
			`INSERT INTO phoneme_scores
			   (item_result_id, expected_phoneme, said_phoneme, accuracy, word_index, phoneme_index, is_omission)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			itemResultID, ph.Expected, nullIfEmpty(ph.Said), ph.Accuracy,
			ph.WordIndex, ph.PhonemeIndex, isOmission,
		)
		if err != nil {
			return nil, fmt.Errorf("insert phoneme score: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit ingest: %w", err)
	}

	// Enqueue async Error Profile recompute (fire-and-forget)
	go s.enqueueErrorProfileRecompute(context.Background(), in.UserID)

	return &IngestResultOutput{
		ItemResultID: itemResultID,
		Accuracy:     accuracy,
		Completeness: completeness,
		Fluency:      fluency,
		Prosody:      prosody,
		TrustFlag:    trustFlag,
	}, nil
}

// IngestBatch processes multiple results (offline sync support — NFR-REL-03).
func (s *SessionService) IngestBatch(ctx context.Context, inputs []IngestResultInput) ([]*IngestResultOutput, error) {
	results := make([]*IngestResultOutput, 0, len(inputs))
	for _, in := range inputs {
		res, err := s.IngestResult(ctx, in)
		if err != nil {
			// Collect errors but continue processing remaining items
			results = append(results, &IngestResultOutput{TrustFlag: "error"})
			continue
		}
		results = append(results, res)
	}
	return results, nil
}

// applyLevelScoring applies Level rules to raw PA scores (§3b.0a).
// easy: ignore trailing consonants (-s/-es/-ed suffixes)
// medium: standard scoring
// hard: full strict scoring
func (s *SessionService) applyLevelScoring(pa domain.PARawPayload, level string) (accuracy, completeness, fluency, prosody *float64) {
	var totalAcc float64
	phonemes := pa.Phonemes

	switch level {
	case "easy":
		// Filter out trailing consonants that are commonly dropped
		filtered := make([]domain.PhonemeScore, 0, len(phonemes))
		for _, ph := range phonemes {
			if ph.IsTrailingConsonant() {
				continue // skip -s/-es/-ed endings
			}
			filtered = append(filtered, ph)
		}
		phonemes = filtered
	case "hard":
		// Use all phonemes including trailing consonants strictly
	default: // medium
		// Use all phonemes, standard threshold
	}

	if len(phonemes) > 0 {
		sum := 0.0
		for _, ph := range phonemes {
			sum += ph.Accuracy
		}
		avg := sum / float64(len(phonemes))
		totalAcc = avg
		accuracy = &totalAcc
	}

	completeness = pa.Completeness
	fluency = pa.Fluency
	prosody = pa.Prosody
	return
}

// isRecordingFailed detects bad recordings (no speech / too short / noise).
func (s *SessionService) isRecordingFailed(pa domain.PARawPayload) bool {
	// If completeness is 0 and no phonemes at all, likely no speech detected
	if len(pa.Phonemes) == 0 && (pa.Completeness == nil || *pa.Completeness == 0) {
		return true
	}
	return false
}

// sanityCheck validates the result against known bounds.
func (s *SessionService) sanityCheck(accuracy, _ *float64, pa domain.PARawPayload) string {
	if accuracy != nil && *accuracy > 99.9 && len(pa.Phonemes) > 5 {
		return "flagged" // suspiciously perfect
	}
	return "ok"
}

// buildMiscue constructs the miscue JSONB payload from PA raw data.
func (s *SessionService) buildMiscue(pa domain.PARawPayload) []map[string]any {
	miscue := make([]map[string]any, 0, len(pa.WordScores))
	for _, ws := range pa.WordScores {
		if ws.ErrorType != "" && ws.ErrorType != "None" {
			miscue = append(miscue, map[string]any{
				"word":       ws.Word,
				"error_type": ws.ErrorType,
				"accuracy":   ws.Accuracy,
				"word_index": ws.WordIndex,
			})
		}
	}
	return miscue
}

func (s *SessionService) logAnalyticsEvent(ctx context.Context, userID uuid.UUID, event string, props map[string]any) {
	propsJSON, _ := json.Marshal(props)
	s.db.Exec(ctx,
		`INSERT INTO analytics_events (user_id, event_name, properties) VALUES ($1, $2, $3::jsonb)`,
		userID, event, string(propsJSON))
}

func (s *SessionService) enqueueErrorProfileRecompute(ctx context.Context, userID uuid.UUID) {
	// TODO: enqueue asynq task TypeErrorProfileRecompute for userID
	_ = ctx
	_ = userID
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

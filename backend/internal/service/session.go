package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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
	TargetItems  *int
}

// SessionDTO is the public session representation.
type SessionDTO struct {
	ID             uuid.UUID  `json:"id"`
	Mode           string     `json:"mode"`
	ScoringLevel   string     `json:"scoring_level"`
	Source         *string    `json:"source,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	SummaryScore   *float64   `json:"summary_score,omitempty"`
	CompletedItems int        `json:"completed_items"`
	TotalItems     *int       `json:"total_items,omitempty"`
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
	db *pgxpool.Pool
	// enqueue có thể nil: đường nội bộ (shadowing chấm điểm lại) không cần hàng đợi, và
	// test dựng service không cần Redis. Mọi chỗ dùng phải kiểm nil.
	enqueue *asynq.Client
}

// NewSessionService creates a SessionService.
func NewSessionService(db *pgxpool.Pool, enqueue *asynq.Client) *SessionService {
	return &SessionService{db: db, enqueue: enqueue}
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
		`INSERT INTO practice_sessions
		   (id, user_id, mode, scoring_level, source, target_item_count, started_at)
		 VALUES ($1, $2, $3, $4, $5, $6, now())`,
		sessionID, in.UserID, in.Mode, scoringLevel, source, in.TargetItems)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	return &SessionDTO{
		ID:             sessionID,
		Mode:           in.Mode,
		ScoringLevel:   scoringLevel,
		Source:         source,
		StartedAt:      time.Now(),
		CompletedItems: 0,
		TotalItems:     in.TargetItems,
	}, nil
}

// Get retrieves a session by ID (verifying ownership).
func (s *SessionService) Get(ctx context.Context, userID uuid.UUID, sessionID string) (*SessionDTO, error) {
	dto := &SessionDTO{}
	err := s.db.QueryRow(ctx,
		`SELECT ps.id, ps.mode, ps.scoring_level, ps.source, ps.started_at, ps.ended_at,
		        ps.summary_score, ps.target_item_count, COUNT(pir.id)::int
		 FROM practice_sessions ps
		 LEFT JOIN practice_item_results pir ON pir.session_id = ps.id
		 WHERE ps.id = $1 AND ps.user_id = $2
		 GROUP BY ps.id`,
		sessionID, userID).Scan(
		&dto.ID, &dto.Mode, &dto.ScoringLevel, &dto.Source,
		&dto.StartedAt, &dto.EndedAt, &dto.SummaryScore,
		&dto.TotalItems, &dto.CompletedItems,
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
		`SELECT ps.id, ps.mode, ps.scoring_level, ps.source, ps.started_at, ps.ended_at,
		        ps.summary_score, ps.target_item_count, COUNT(pir.id)::int
		 FROM practice_sessions ps
		 LEFT JOIN practice_item_results pir ON pir.session_id = ps.id
		 WHERE ps.user_id = $1
		 GROUP BY ps.id
		 ORDER BY ps.started_at DESC
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
			&dto.StartedAt, &dto.EndedAt, &dto.SummaryScore,
			&dto.TotalItems, &dto.CompletedItems); err != nil {
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
		`SELECT ps.id, ps.mode, ps.scoring_level, ps.source, ps.started_at, ps.ended_at,
		        ps.summary_score, ps.target_item_count, COUNT(pir.id)::int
		 FROM practice_sessions ps
		 LEFT JOIN practice_item_results pir ON pir.session_id = ps.id
		 WHERE ps.user_id = $1 AND ps.ended_at IS NULL
		 GROUP BY ps.id
		 ORDER BY ps.started_at DESC LIMIT 1`,
		userID).Scan(
		&dto.ID, &dto.Mode, &dto.ScoringLevel, &dto.Source,
		&dto.StartedAt, &dto.EndedAt, &dto.SummaryScore,
		&dto.TotalItems, &dto.CompletedItems,
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
	// Verify session belongs to user
	var sessionMode string
	err := s.db.QueryRow(ctx,
		`SELECT mode FROM practice_sessions WHERE id = $1 AND user_id = $2`,
		in.SessionID, in.UserID).Scan(&sessionMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find session: %w", err)
	}

	if existing, err := s.findIngestedResult(ctx, in.SessionID, in.IdempotencyKey); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
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
		   (id, session_id, content_item_id, minimal_pair_id, scoring_level, idempotency_key,
		    accuracy, fluency, completeness, prosody, miscue, audio_ref, trust_flag)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		itemResultID, in.SessionID, contentItemID, minimalPairID, in.ScoringLevel,
		in.IdempotencyKey, accuracy, fluency, completeness, prosody, string(miscueJSON),
		nullIfEmpty(in.AudioRef), trustFlag,
	)
	if err != nil {
		_ = tx.Rollback(ctx)
		if existing, findErr := s.findIngestedResult(
			ctx, in.SessionID, in.IdempotencyKey,
		); findErr == nil && existing != nil {
			return existing, nil
		}
		return nil, fmt.Errorf("insert item result: %w", err)
	}

	// Persist phoneme scores (always, regardless of Level — BR-LEVEL-05)
	for _, ph := range in.PARaw.Phonemes {
		diagnosis := diagnosisFromLegacyPA(ph)
		_, err = tx.Exec(ctx,
			`INSERT INTO phoneme_scores
			   (item_result_id, expected_phoneme, said_phoneme, accuracy, word_index,
			    phoneme_index, is_omission, diagnosis)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			itemResultID, ph.Expected, nullIfEmpty(ph.Said), ph.Accuracy,
			ph.WordIndex, ph.PhonemeIndex,
			diagnosis == domain.DiagOmission, // is_omission: DEPRECATED, dẫn xuất
			string(diagnosis),
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

	// Tiến độ thử thách ngày cập nhật NGAY, không qua hàng đợi: người học vừa làm xong
	// mục cuối sẽ quay lại màn hình thử thách trong vài giây, và thấy nó vẫn "chưa xong"
	// là lỗi hiển nhiên. Một câu lệnh, đủ nhanh để chạy thẳng.
	//
	// Lỗi chỉ log — kết quả luyện đã lưu xong, không được để việc phụ này làm hỏng nó.
	if err := NewDailyService(s.db).MarkDailyProgress(ctx, in.UserID); err != nil {
		slog.Error("cập nhật tiến độ thử thách", "user_id", in.UserID, "err", err)
	}

	return &IngestResultOutput{
		ItemResultID: itemResultID,
		Accuracy:     accuracy,
		Completeness: completeness,
		Fluency:      fluency,
		Prosody:      prosody,
		TrustFlag:    trustFlag,
	}, nil
}

func (s *SessionService) findIngestedResult(
	ctx context.Context, sessionID, idempotencyKey string,
) (*IngestResultOutput, error) {
	var out IngestResultOutput
	err := s.db.QueryRow(ctx,
		`SELECT id, accuracy, completeness, fluency, prosody, trust_flag
		   FROM practice_item_results
		  WHERE session_id = $1 AND idempotency_key = $2`,
		sessionID, idempotencyKey,
	).Scan(
		&out.ItemResultID, &out.Accuracy, &out.Completeness,
		&out.Fluency, &out.Prosody, &out.TrustFlag,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find ingested result: %w", err)
	}
	return &out, nil
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

// diagnosisFromLegacyPA suy chẩn đoán từ payload Azure cũ.
//
// Trước đây code viết `isOmission := ph.Said == "" || ph.Accuracy < 10`, gộp ba tình
// huống khác hẳn nhau làm một:
//   - engine KHÔNG BIẾT user nói gì  → phải là uncertain
//   - user thực sự nuốt âm            → omission
//   - user nói sai âm nhưng điểm thấp → substitution
//
// Với engine self-host, Diagnosis được trả về tường minh và hàm này không dùng tới.
// Nó chỉ phục vụ luồng Azure cũ trong giai đoạn chuyển tiếp, và cố tình THẬN TRỌNG:
// khi không có bằng chứng thì trả uncertain, không đoán thành omission.
func diagnosisFromLegacyPA(ph domain.PhonemeScore) domain.PhonemeDiagnosis {
	if ph.Diagnosis != "" {
		return ph.Diagnosis // engine mới đã nói rõ
	}
	switch {
	case ph.ErrorType == string(domain.DiagOmission):
		return domain.DiagOmission
	case ph.Said == "":
		// Không biết user nói gì. KHÔNG suy ra omission — đó chính là lỗi cũ.
		return domain.DiagUncertain
	case ph.Said != ph.Expected:
		return domain.DiagSubstitution
	default:
		return domain.DiagCorrect
	}
}

// buildMiscue constructs the miscue JSONB payload from PA raw data.
//
// Trước đây hàm này bỏ qua mọi từ không có ErrorType, tức là coi "engine không chẩn đoán
// được" đồng nghĩa với "user phát âm đúng". Fix Guide vì thế mất sạch dữ liệu đầu vào ở
// đúng những ca engine đang lưỡng lự. Nay giữ lại chúng với error_type = "uncertain".
func (s *SessionService) buildMiscue(pa domain.PARawPayload) []map[string]any {
	miscue := make([]map[string]any, 0, len(pa.WordScores))
	for _, ws := range pa.WordScores {
		errorType := ws.ErrorType
		switch errorType {
		case "None":
			continue // engine khẳng định đúng — bỏ qua là chính xác
		case "":
			errorType = string(domain.DiagUncertain) // engine không kết luận được
		}
		miscue = append(miscue, map[string]any{
			"word":       ws.Word,
			"error_type": errorType,
			"accuracy":   ws.Accuracy,
			"word_index": ws.WordIndex,
		})
	}
	return miscue
}

func (s *SessionService) logAnalyticsEvent(ctx context.Context, userID uuid.UUID, event string, props map[string]any) {
	propsJSON, _ := json.Marshal(props)
	s.db.Exec(ctx,
		`INSERT INTO analytics_events (user_id, event_name, properties) VALUES ($1, $2, $3::jsonb)`,
		userID, event, string(propsJSON))
}

// enqueueErrorProfileRecompute đẩy task tính lại hồ sơ lỗi.
//
// KHÔNG chặn luồng trả kết quả: người học vừa đọc xong cần thấy điểm ngay, còn hồ sơ lỗi
// cập nhật chậm vài giây không ai nhận ra. Vì vậy lỗi ở đây chỉ log, không trả ngược.
//
// `asynq.Unique` gom một chuỗi kết quả liên tiếp thành MỘT lần tính. Một phiên luyện 20 câu
// sẽ đẩy 20 task giống hệt nhau; thiếu dedup thì 19 lần tính đầu bị 20 lần sau ghi đè, tốn
// CPU và I/O cho kết quả bị vứt đi. TTL nhỏ hơn nhịp đọc thực tế nên lần cuối luôn chạy.
func (s *SessionService) enqueueErrorProfileRecompute(ctx context.Context, userID uuid.UUID) {
	if s.enqueue == nil {
		return // đường không có worker (test, hoặc host chỉ chạy API)
	}

	payload, err := json.Marshal(ErrorProfilePayload{UserID: userID.String()})
	if err != nil {
		slog.Error("marshal payload error profile", "user_id", userID, "err", err)
		return
	}

	task := asynq.NewTask(TypeErrorProfileRecompute, payload, asynq.Queue("default"))
	if _, err := s.enqueue.EnqueueContext(ctx, task,
		asynq.MaxRetry(3),
		asynq.Timeout(errorProfileTimeout),
		// Hoãn một nhịp để gom cả cụm kết quả của cùng một phiên vào một lần tính.
		asynq.ProcessIn(errorProfileDebounce),
		asynq.Unique(errorProfileDebounce+errorProfileTimeout),
	); err != nil && !errors.Is(err, asynq.ErrDuplicateTask) {
		// Trùng task là KẾT QUẢ MONG MUỐN của Unique, không phải sự cố — chỉ log cái khác.
		slog.Error("đẩy task error profile", "user_id", userID, "err", err)
	}
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

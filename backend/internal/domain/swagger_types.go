package domain

import "time"

// ─── Auth ────────────────────────────────────────────────────────────────────

// TokenData là data trả về sau đăng ký / đăng nhập / refresh.
type TokenData struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	UserID       string    `json:"user_id"`
	IsGuest      bool      `json:"is_guest,omitempty"`
}

// ─── System ──────────────────────────────────────────────────────────────────

// MessageData là data trả về cho các action không có payload (delete, export…).
type MessageData struct {
	Message string `json:"message"`
}

// StatusData là data cho probes (health/ready).
type StatusData struct {
	Status string `json:"status"`
	DB     string `json:"db,omitempty"`
	Redis  string `json:"redis,omitempty"`
}

// AppConfigData là map key-value của app config flags.
type AppConfigData map[string]interface{}

// LegalDocData là nội dung một legal document.
type LegalDocData struct {
	ID        string  `json:"id"`
	DocType   string  `json:"doc_type"`
	Version   int     `json:"version"`
	ContentMD string  `json:"content_md"`
	Locale    string  `json:"locale"`
}

// ─── Coach / Progress ────────────────────────────────────────────────────────

// RecommendationItem là một bài luyện được đề xuất.
type RecommendationItem struct {
	ContentID  string `json:"content_id,omitempty"`
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	Difficulty int    `json:"difficulty,omitempty"`
	Reason     string `json:"reason"`
	Message    string `json:"message,omitempty"`
}

// ReportData là kết quả báo cáo tiến độ tuần/tháng.
type ReportData struct {
	Period    string          `json:"period"`
	Snapshots []SnapshotPoint `json:"snapshots"`
}

// SnapshotPoint là một điểm trên biểu đồ tiến độ.
type SnapshotPoint struct {
	Date  string   `json:"date"`
	Score *float64 `json:"score"`
}

// ProgressOverviewData là dữ liệu tổng quan tiến độ.
type ProgressOverviewData struct {
	CurrentStreak  int     `json:"current_streak"`
	LongestStreak  int     `json:"longest_streak"`
	LastActiveDate *string `json:"last_active_date"`
	TotalSessions  int     `json:"total_sessions"`
}

// ChartsData là dữ liệu biểu đồ điểm theo thời gian.
type ChartsData struct {
	Period string          `json:"period"`
	Points []SnapshotPoint `json:"points"`
}

// BadgeListData chứa badges earned và locked.
type BadgeListData struct {
	Earned []BadgeItem `json:"earned"`
	Locked []BadgeItem `json:"locked"`
}

// BadgeItem là thông tin một badge.
type BadgeItem struct {
	ID            string     `json:"id"`
	Code          string     `json:"code"`
	NameVI        string     `json:"name_vi"`
	DescriptionVI string     `json:"description_vi"`
	IconURL       string     `json:"icon_url,omitempty"`
	CriteriaType  string     `json:"criteria_type"`
	CriteriaValue int        `json:"criteria_value"`
	EarnedAt      *time.Time `json:"earned_at,omitempty"`
}

// ─── Sessions ────────────────────────────────────────────────────────────────

// SyncData là payload đồng bộ đa thiết bị.
type SyncData struct {
	Profile   interface{} `json:"profile"`
	SyncedAt  time.Time   `json:"synced_at"`
}

// ─── Daily ───────────────────────────────────────────────────────────────────

// DailyChallengeData là nội dung daily challenge.
type DailyChallengeData struct {
	ChallengeID   string  `json:"challenge_id,omitempty"`
	Date          string  `json:"date"`
	PassageID     *string `json:"passage_id,omitempty"`
	ContentItemID *string `json:"content_item_id,omitempty"`
	Category      string  `json:"category,omitempty"`
	BannerURL     *string `json:"banner_url,omitempty"`
	Moderated     bool    `json:"moderated"`
	UserStatus    *string `json:"user_status,omitempty"`
	Available     bool    `json:"available,omitempty"`
	Message       string  `json:"message,omitempty"`
}

// DailyHistoryItem là một entry trong lịch sử daily challenge.
type DailyHistoryItem struct {
	Date        string     `json:"date"`
	Category    string     `json:"category"`
	Status      string     `json:"status"`
	Score       *float64   `json:"score,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// ─── Exam ─────────────────────────────────────────────────────────────────────

// ExamPromptItem là thông tin một đề thi.
type ExamPromptItem struct {
	ID           string `json:"id"`
	ExamType     string `json:"exam_type"`
	Part         string `json:"part"`
	PromptText   string `json:"prompt_text"`
	PrepSeconds  int    `json:"prep_seconds"`
	SpeakSeconds int    `json:"speak_seconds"`
}

// ExamSessionData là thông tin một exam session.
type ExamSessionData struct {
	SessionID string  `json:"session_id"`
	ExamType  string  `json:"exam_type"`
	Status    string  `json:"status"`
	BandScore *float64 `json:"band_score,omitempty"`
	CEFRLevel *string  `json:"cefr_level,omitempty"`
}

// ExamReportData là báo cáo đầy đủ của một exam session.
type ExamReportData struct {
	SessionID string      `json:"session_id"`
	ExamType  string      `json:"exam_type"`
	Status    string      `json:"status"`
	BandScore *float64    `json:"band_score,omitempty"`
	CEFRLevel *string     `json:"cefr_level,omitempty"`
	Criteria  interface{} `json:"criteria,omitempty"`
}

// ─── Subscription ─────────────────────────────────────────────────────────────

// PlanItem là thông tin một gói đăng ký.
type PlanItem struct {
	Plan             string      `json:"plan"`
	ProductIDIOS     string      `json:"product_id_ios"`
	ProductIDAndroid string      `json:"product_id_android"`
	BillingPeriod    string      `json:"billing_period"`
	PriceVND         int         `json:"price_vnd"`
	DisplayNameVI    string      `json:"display_name_vi"`
	Features         interface{} `json:"features"`
}

// ─── Minimal Pairs ────────────────────────────────────────────────────────────

// ListenDrillData là data khởi tạo một listen drill.
type ListenDrillData struct {
	DrillID    string          `json:"drill_id"`
	TotalItems int             `json:"total_items"`
	HeartsLeft int             `json:"hearts_left"`
	Pairs      []ListenPairItem `json:"pairs"`
}

// ListenPairItem là một cặp âm trong drill.
type ListenPairItem struct {
	PairID  string  `json:"pair_id"`
	WordA   string  `json:"word_a"`
	WordB   string  `json:"word_b"`
	AudioA  *string `json:"audio_a,omitempty"`
	AudioB  *string `json:"audio_b,omitempty"`
}

// AnswerResultData là kết quả sau khi nộp đáp án.
type AnswerResultData struct {
	IsCorrect  bool   `json:"is_correct"`
	PlayedWord string `json:"played_word"`
}

// DrillStatusData là trạng thái drill.
type DrillStatusData struct {
	DrillID      string `json:"drill_id"`
	TotalItems   int    `json:"total_items"`
	CorrectCount int    `json:"correct_count"`
	HeartsLeft   int    `json:"hearts_left"`
	Status       string `json:"status"`
}

// ShadowingCompleteData là tổng kết khi hoàn thành passage.
type ShadowingCompleteData struct {
	PassageID string   `json:"passage_id"`
	Completed bool     `json:"completed"`
	AvgScore  *float64 `json:"avg_score,omitempty"`
}

// ─── Typed Response wrappers (dùng trong @Success annotations) ────────────────
// swaggo đọc generic syntax `domain.Response{data=X}` nhưng cần X là named type.
// Những wrapper dưới đây giúp Swagger UI render đúng schema.

// TokenResponse là response envelope cho auth endpoints.
type TokenResponse struct {
	Data  TokenData `json:"data"`
	Error string    `json:"error,omitempty"`
}

// UserProfileResponse là response envelope cho GET/PATCH /me.
type UserProfileResponse struct {
	Data  interface{} `json:"data"` // service.UserProfile
	Error string      `json:"error,omitempty"`
}

// MessageResponse là response envelope cho action endpoints.
type MessageResponse struct {
	Data  MessageData `json:"data"`
	Error string      `json:"error,omitempty"`
}

// StatusResponse là response envelope cho health/ready.
type StatusResponse struct {
	Data  StatusData `json:"data"`
	Error string     `json:"error,omitempty"`
}

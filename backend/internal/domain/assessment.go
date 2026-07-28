package domain

// Kết quả chấm phát âm, trung lập với engine.
//
// Đây là bản Go của contract §2.1 trong PRONUNCIATION_ENGINE_PLAN.md. Nó KHÔNG phải
// PARawPayload của Azure — xem pa.go, kiểu đó nay chỉ còn phục vụ luồng Azure cũ trong
// giai đoạn chuyển tiếp.
//
// Hai nguyên tắc mã hoá thẳng vào kiểu:
//   1. Trường không đo được là con trỏ nil, không phải 0. Capabilities cho biết cái nào
//      vắng vì engine không hỗ trợ.
//   2. SaidPhoneme nil nghĩa là CHƯA XÁC ĐỊNH, không phải omission. Diagnosis mới là
//      nguồn sự thật về loại lỗi.

// PhonemeDiagnosis phân loại từng âm vị. Nguồn sự thật, thay cho suy diễn từ chuỗi rỗng.
type PhonemeDiagnosis string

const (
	DiagCorrect      PhonemeDiagnosis = "correct"
	DiagSubstitution PhonemeDiagnosis = "substitution"
	DiagOmission     PhonemeDiagnosis = "omission"
	DiagInsertion    PhonemeDiagnosis = "insertion"
	// DiagUncertain: engine nghe thấy gì đó nhưng không đủ tự tin để kết luận.
	// KHÔNG được quy về omission — đó là hai tình huống khác nhau với user.
	DiagUncertain PhonemeDiagnosis = "uncertain"
)

// WordDiagnosis phân loại ở mức từ, suy ra từ các âm vị con theo precedence cố định.
type WordDiagnosis string

const (
	WordCorrect          WordDiagnosis = "correct"
	WordMispronunciation WordDiagnosis = "mispronunciation"
	WordOmission         WordDiagnosis = "omission"
	WordUncertain        WordDiagnosis = "uncertain"
)

// Capability là năng lực engine tự khai báo. Thiếu capability nghĩa là engine KHÔNG ĐO
// ĐƯỢC chiều đó — khác hẳn với đo được và ra 0.
type Capability string

const (
	CapPhoneAccuracy  Capability = "phone_accuracy"
	CapPhoneDiagnosis Capability = "phone_diagnosis"
	CapWordAccuracy   Capability = "word_accuracy"
	CapWordDiagnosis  Capability = "word_diagnosis"
	CapCompleteness   Capability = "completeness"
	CapFluency        Capability = "fluency"
	CapProsody        Capability = "prosody"
)

// AssessmentPhoneme là điểm và chẩn đoán cho một âm vị.
type AssessmentPhoneme struct {
	Expected     string           `json:"expected"`
	Said         *string          `json:"said"`
	WordIndex    int              `json:"word_index"`
	PhonemeIndex int              `json:"phoneme_index"`
	Accuracy     *float64         `json:"accuracy"`
	GOPRaw       float64          `json:"gop_raw"`
	Diagnosis    PhonemeDiagnosis `json:"diagnosis"`
	Confidence   float64          `json:"confidence"`
}

// AssessmentWord là điểm và chẩn đoán cho một từ.
type AssessmentWord struct {
	Word      string        `json:"word"`
	WordIndex int           `json:"word_index"`
	Accuracy  *float64      `json:"accuracy"`
	Diagnosis WordDiagnosis `json:"diagnosis"`
}

// AssessmentOverall là điểm toàn câu.
//
// Accuracy nil khi không có âm vị nào chấm được (đọc sót toàn bộ) — KHÔNG phải 0.
// Trả 0 sẽ nói dối rằng đã chấm và user bị điểm liệt, trong khi sự thật là không có gì
// để chấm. Completeness mới là chiều đo việc đọc sót.
type AssessmentOverall struct {
	Accuracy     *float64 `json:"accuracy"`
	Fluency      *float64 `json:"fluency"`
	Completeness *float64 `json:"completeness"`
	Prosody      *float64 `json:"prosody"`
}

// AssessmentAudio mô tả audio đã chấm.
type AssessmentAudio struct {
	DurationMs int `json:"duration_ms"`
	SampleRate int `json:"sample_rate"`
}

// AssessmentTiming là thời gian xử lý từng chặng, dùng cho monitoring.
type AssessmentTiming struct {
	Total     int `json:"total"`
	Forward   int `json:"forward"`
	GOP       int `json:"gop"`
	Diagnosis int `json:"diagnosis"`
}

// NormalizedAssessmentResult là toàn bộ kết quả engine trả về.
//
// Bốn trường version là bắt buộc: biểu đồ tiến bộ của user phải lọc theo chúng, nếu
// không một lần đổi calibration sẽ tạo bước nhảy giả trong đồ thị.
type NormalizedAssessmentResult struct {
	Engine             string              `json:"engine"`
	ModelVersion       string              `json:"model_version"`
	G2PVersion         string              `json:"g2p_version"`
	AlgorithmVersion   string              `json:"algorithm_version"`
	CalibrationVersion string              `json:"calibration_version"`
	Capabilities       []Capability        `json:"capabilities"`
	Audio              AssessmentAudio     `json:"audio"`
	TimingMs           AssessmentTiming    `json:"timing_ms"`
	Overall            AssessmentOverall   `json:"overall"`
	Words              []AssessmentWord    `json:"words"`
	Phonemes           []AssessmentPhoneme `json:"phonemes"`
}

// Supports cho biết engine có đo được chiều này không.
func (r *NormalizedAssessmentResult) Supports(c Capability) bool {
	for _, have := range r.Capabilities {
		if have == c {
			return true
		}
	}
	return false
}

// EngineErrorCode là mã lỗi engine trả về. Phân loại retry được hay không nằm ở
// Retryable — worker dựa vào đó, không đoán theo HTTP status.
type EngineErrorCode string

const (
	EngErrAudioTooShort     EngineErrorCode = "audio_too_short"
	EngErrAudioTooLong      EngineErrorCode = "audio_too_long"
	EngErrNoSpeechDetected  EngineErrorCode = "no_speech_detected"
	EngErrG2PFailed         EngineErrorCode = "g2p_failed"
	EngErrModelOverloaded   EngineErrorCode = "model_overloaded"
	EngErrInternal          EngineErrorCode = "internal"
)

// EngineError là thân lỗi engine trả về.
type EngineError struct {
	Code      EngineErrorCode `json:"code"`
	Message   string          `json:"message"`
	Retryable bool            `json:"retryable"`
}

func (e *EngineError) Error() string { return string(e.Code) + ": " + e.Message }

// IsUserFacing cho biết lỗi này nên hiển thị cho user hay chỉ ghi log.
// Lỗi audio là do user (nói quá ngắn, không có tiếng) — phải nói cho họ biết để thử lại.
// Lỗi hệ thống thì không, user không làm gì được với nó.
func (e *EngineError) IsUserFacing() bool {
	switch e.Code {
	case EngErrAudioTooShort, EngErrAudioTooLong, EngErrNoSpeechDetected:
		return true
	default:
		return false
	}
}

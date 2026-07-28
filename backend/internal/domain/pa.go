package domain

// PARawPayload is the raw pronunciation assessment result received from Azure Speech SDK.
type PARawPayload struct {
	WordScores   []WordScore    `json:"word_scores"`
	Phonemes     []PhonemeScore `json:"phonemes"`
	Fluency      *float64       `json:"fluency"`
	Completeness *float64       `json:"completeness"`
	Prosody      *float64       `json:"prosody"`
}

// WordScore is per-word assessment data from Azure.
type WordScore struct {
	Word      string  `json:"word"`
	Accuracy  float64 `json:"accuracy"`
	ErrorType string  `json:"error_type"` // Omission, Insertion, Mispronunciation, None
	WordIndex int     `json:"word_index"`
}

// PhonemeScore is per-phoneme data from Azure NBest.
type PhonemeScore struct {
	Expected     string  `json:"expected"`
	Said         string  `json:"said"` // NBest: what the user actually said
	Accuracy     float64 `json:"accuracy"`
	WordIndex    int     `json:"word_index"`
	PhonemeIndex int     `json:"phoneme_index"`

	// Diagnosis là chẩn đoán tường minh. Rỗng nghĩa là client không cung cấp — khi đó
	// service suy ra một cách THẬN TRỌNG (xem diagnosisFromLegacyPA), không đoán bừa
	// thành omission như code cũ.
	Diagnosis PhonemeDiagnosis `json:"diagnosis,omitempty"`
	// ErrorType tuỳ chọn ở mức âm vị, dùng khi client biết rõ loại lỗi.
	ErrorType string `json:"error_type,omitempty"`
}

// trailingConsonants are phonemes commonly dropped by Vietnamese speakers at word end.
// These are skipped when scoring_level = "easy" (BR-LEVEL-02..04).
var trailingConsonants = map[string]bool{
	"s": true, "z": true, "t": true, "d": true, "ɪd": true, "əd": true,
}

// IsTrailingConsonant returns true if this phoneme corresponds to a common
// trailing consonant (-s/-es/-ed endings) that should be ignored in "easy" level.
func (p PhonemeScore) IsTrailingConsonant() bool {
	return trailingConsonants[p.Expected]
}

package worker

import (
	"encoding/json"
	"testing"

	"github.com/phonara/backend/internal/domain"
	"github.com/phonara/backend/internal/service"
)

// TypeAssessmentRun được khai báo ở hai nơi: service (nơi enqueue) và worker (nơi
// đăng ký handler). Lệch nhau thì task được đẩy vào hàng đợi nhưng KHÔNG AI xử lý —
// job nằm pending mãi mãi, client poll mãi không có kết quả, và không có lỗi nào được
// ghi ra. Test này biến lỗi im lặng đó thành lỗi biên dịch được phát hiện ngay.
func TestAssessmentTaskTypeMatchesService(t *testing.T) {
	if TypeAssessmentRun != service.TypeAssessmentRun {
		t.Fatalf("task type lệch: worker=%q service=%q — task sẽ không bao giờ được xử lý",
			TypeAssessmentRun, service.TypeAssessmentRun)
	}
}

// Cùng lý do với test trên, cho task tính lại hồ sơ lỗi.
func TestErrorProfileTaskTypeMatchesService(t *testing.T) {
	if TypeErrorProfileRecompute != service.TypeErrorProfileRecompute {
		t.Fatalf("task type lệch: worker=%q service=%q — hồ sơ lỗi sẽ không bao giờ được tính",
			TypeErrorProfileRecompute, service.TypeErrorProfileRecompute)
	}
}

func TestBuildMiscueKeepsUncertain(t *testing.T) {
	// Bỏ `uncertain` khỏi miscue sẽ khiến Fix Guide hiểu "engine không kết luận được"
	// thành "user phát âm đúng" — đúng lỗi đã có trong code cũ.
	r := &domain.NormalizedAssessmentResult{
		Words: []domain.AssessmentWord{
			{Word: "three", WordIndex: 0, Diagnosis: domain.WordMispronunciation},
			{Word: "blue", WordIndex: 1, Diagnosis: domain.WordUncertain},
			{Word: "cat", WordIndex: 2, Diagnosis: domain.WordCorrect},
			{Word: "sells", WordIndex: 3, Diagnosis: domain.WordOmission},
		},
	}

	miscue := buildMiscueFromResult(r)
	if len(miscue) != 3 {
		t.Fatalf("kỳ vọng 3 mục (bỏ 'correct'), nhận %d: %+v", len(miscue), miscue)
	}

	got := map[string]string{}
	for _, m := range miscue {
		got[m["word"].(string)] = m["error_type"].(string)
	}
	for word, want := range map[string]string{
		"three": string(domain.WordMispronunciation),
		"blue":  string(domain.WordUncertain),
		"sells": string(domain.WordOmission),
	} {
		if got[word] != want {
			t.Errorf("%s: kỳ vọng %q, nhận %q", word, want, got[word])
		}
	}
	if _, present := got["cat"]; present {
		t.Error("từ đúng không được vào miscue")
	}
}

func TestEngineErrorUserFacing(t *testing.T) {
	// Lỗi do user thì phải nói cho họ biết để thử lại; lỗi hệ thống thì không —
	// user không làm gì được, và message nội bộ có thể lộ cấu trúc hệ thống.
	cases := map[domain.EngineErrorCode]bool{
		domain.EngErrAudioTooShort:    true,
		domain.EngErrAudioTooLong:     true,
		domain.EngErrNoSpeechDetected: true,
		domain.EngErrG2PFailed:        false,
		domain.EngErrModelOverloaded:  false,
		domain.EngErrInternal:         false,
	}
	for code, want := range cases {
		e := &domain.EngineError{Code: code}
		if got := e.IsUserFacing(); got != want {
			t.Errorf("%s: kỳ vọng IsUserFacing=%v, nhận %v", code, want, got)
		}
	}
}

func TestAssessmentJobPayloadRoundTrip(t *testing.T) {
	in := service.AssessmentJobPayload{JobID: "8f14e45f-ceea-467a-9b8a-8b1c9d2f3a4b"}
	blob, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out service.AssessmentJobPayload
	if err := json.Unmarshal(blob, &out); err != nil {
		t.Fatal(err)
	}
	if out.JobID != in.JobID {
		t.Fatalf("round-trip hỏng: %q != %q", out.JobID, in.JobID)
	}
}

func TestCapabilitiesGate(t *testing.T) {
	// Capabilities là cách phân biệt "engine không đo được" với "đo được và bằng 0".
	r := &domain.NormalizedAssessmentResult{
		Capabilities: []domain.Capability{
			domain.CapPhoneAccuracy, domain.CapCompleteness,
		},
	}
	if !r.Supports(domain.CapPhoneAccuracy) {
		t.Error("phải hỗ trợ phone_accuracy")
	}
	if r.Supports(domain.CapFluency) {
		t.Error("KHÔNG được báo hỗ trợ fluency — engine v1 không đo được")
	}
}

func TestRetryExhausted(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		retried  int
		maxRetry int
		want     bool
	}{
		{name: "first attempt", retried: 0, maxRetry: 3, want: false},
		{name: "last retry", retried: 3, maxRetry: 3, want: true},
		{name: "defensive overflow", retried: 4, maxRetry: 3, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := retryExhausted(tt.retried, tt.maxRetry); got != tt.want {
				t.Fatalf("retryExhausted(%d, %d) = %v, want %v",
					tt.retried, tt.maxRetry, got, tt.want)
			}
		})
	}
}

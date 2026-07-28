package service

import (
	"math"
	"testing"
)

func ptrF(v float64) *float64 { return &v }
func ptrS(v string) *string   { return &v }

// ── quy điểm một lần chấm ─────────────────────────────────────────────────────

func TestObservationScoring(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		obs     phonemeObservation
		want    float64
		counted bool
		why     string
	}{
		{
			name:    "âm đúng dùng accuracy",
			obs:     phonemeObservation{Accuracy: ptrF(88), Diagnosis: ptrS("correct")},
			want:    88, counted: true,
		},
		{
			name:    "thay thế vẫn tính, điểm thấp",
			obs:     phonemeObservation{Accuracy: ptrF(31), Diagnosis: ptrS("substitution")},
			want:    31, counted: true,
		},
		{
			name:    "nuốt âm = 0, dù accuracy là NULL",
			obs:     phonemeObservation{Accuracy: nil, Diagnosis: ptrS("omission")},
			want:    0, counted: true,
			why: "nuốt phụ âm cuối là lỗi phổ biến nhất; bỏ qua nó thì mastery của chính " +
				"âm bị nuốt sẽ trông đẹp một cách sai lệch",
		},
		{
			name:    "âm thừa không quy được cho ai",
			obs:     phonemeObservation{Accuracy: ptrF(70), Diagnosis: ptrS("insertion")},
			counted: false,
		},
		{
			name:    "uncertain VẪN tính bằng accuracy của nó",
			obs:     phonemeObservation{Accuracy: ptrF(64), Diagnosis: ptrS("uncertain")},
			want:    64, counted: true,
			why: "bất định nằm ở NHÃN chứ không ở ĐIỂM — cùng quy tắc với " +
				"aggregate.mean_accuracy của engine",
		},
		{
			name:    "bản ghi cũ: diagnosis NULL + is_omission",
			obs:     phonemeObservation{Accuracy: nil, IsOmission: true},
			want:    0, counted: true,
			why: "dữ liệu trước migration 000007 mang ngữ nghĩa omission ở cờ boolean",
		},
		{
			name:    "bản ghi cũ: diagnosis NULL, có accuracy",
			obs:     phonemeObservation{Accuracy: ptrF(75)},
			want:    75, counted: true,
		},
		{
			name:    "không có gì để chấm thì bỏ",
			obs:     phonemeObservation{Accuracy: nil, Diagnosis: ptrS("correct")},
			counted: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, counted := tc.obs.score()
			if counted != tc.counted {
				t.Fatalf("counted = %v, want %v. %s", counted, tc.counted, tc.why)
			}
			if counted && got != tc.want {
				t.Fatalf("score = %v, want %v. %s", got, tc.want, tc.why)
			}
		})
	}
}

// ── EWMA ──────────────────────────────────────────────────────────────────────

func TestEWMAFirstObservationSeedsValue(t *testing.T) {
	t.Parallel()
	// Không khởi tạo từ 0: người học mới sẽ phải đọc đúng nhiều lần chỉ để leo về đúng
	// mức thật của mình, và biểu đồ tiến bộ sẽ bắt đầu bằng một cú dốc giả.
	if got := ewma([]float64{82}); got != 82 {
		t.Fatalf("ewma một quan sát = %v, want 82", got)
	}
}

func TestEWMAWeightsRecentHigher(t *testing.T) {
	t.Parallel()
	improving := ewma([]float64{20, 20, 20, 90})
	declining := ewma([]float64{90, 20, 20, 20})
	if improving <= declining {
		t.Fatalf("tiến bộ (%v) phải cao hơn sa sút (%v) trên cùng tập giá trị",
			improving, declining)
	}
}

func TestEWMAOneBadTakeDoesNotWipeHistory(t *testing.T) {
	t.Parallel()
	// Micro trục trặc một lần không được đánh sập thành quả nhiều buổi luyện.
	steady := []float64{90, 90, 90, 90, 90}
	withGlitch := append(append([]float64{}, steady...), 0)

	got := ewma(withGlitch)
	if got < 60 {
		t.Fatalf("một lần 0 điểm kéo mastery xuống %v — quá nhạy, người học sẽ mất niềm tin", got)
	}
	if got >= ewma(steady) {
		t.Fatalf("một lần 0 điểm phải kéo mastery XUỐNG, được %v", got)
	}
}

func TestEWMAEmpty(t *testing.T) {
	t.Parallel()
	if got := ewma(nil); got != 0 {
		t.Fatalf("ewma rỗng = %v, want 0", got)
	}
}

func TestEWMATruncationWindowIsNumericallyIrrelevant(t *testing.T) {
	t.Parallel()
	// Lý lẽ cho maxObservationsPerPhoneme: cắt ở đó phải KHÔNG đổi kết quả một cách đáng
	// kể, nếu không thì nó là đánh đổi chứ không phải tối ưu. Test giữ lý lẽ đó đúng nếu
	// ai đó chỉnh ewmaAlpha.
	long := make([]float64, maxObservationsPerPhoneme*3)
	for i := range long {
		long[i] = 100
	}
	long[0] = 0 // giá trị cực đoan ở tận cùng quá khứ

	truncated := long[len(long)-maxObservationsPerPhoneme:]
	if diff := math.Abs(ewma(long) - ewma(truncated)); diff > 1e-6 {
		t.Fatalf("cắt cửa sổ đổi kết quả %v — cửa sổ không còn vô hại, xem lại ewmaAlpha", diff)
	}
}

// ── phân loại trạng thái ──────────────────────────────────────────────────────

func TestMasteryStatusBoundaries(t *testing.T) {
	t.Parallel()
	cases := map[float64]string{
		0: "weak", 59.9: "weak",
		60: "improving", 79.9: "improving",
		80: "good", 100: "good",
	}
	for mastery, want := range cases {
		if got := masteryStatus(mastery); got != want {
			t.Errorf("masteryStatus(%v) = %q, want %q", mastery, got, want)
		}
	}
}

// ── top_errors ────────────────────────────────────────────────────────────────

func TestTopErrorsIgnoresThinEvidence(t *testing.T) {
	t.Parallel()
	// Một lần đọc hỏng không được đẩy một âm vào Fix Guide.
	got := buildTopErrors(map[string]masteryValue{
		"θ": {Mastery: 5, Attempts: minAttemptsForTopError - 1},
	})
	if len(got) != 0 {
		t.Fatalf("bằng chứng mỏng vẫn lọt vào top_errors: %+v", got)
	}
}

func TestTopErrorsOnlyWeak(t *testing.T) {
	t.Parallel()
	got := buildTopErrors(map[string]masteryValue{
		"θ": {Mastery: 30, Attempts: 10},
		"s": {Mastery: 85, Attempts: 10},
	})
	if len(got) != 1 || got[0].Phoneme != "θ" {
		t.Fatalf("chỉ âm yếu mới được vào top_errors, được %+v", got)
	}
}

func TestTopErrorsSortedAndCapped(t *testing.T) {
	t.Parallel()
	in := map[string]masteryValue{}
	for i, p := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		in[p] = masteryValue{Mastery: float64(i), Attempts: 10}
	}
	got := buildTopErrors(in)
	if len(got) != topErrorsLimit {
		t.Fatalf("len = %d, want %d", len(got), topErrorsLimit)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Mastery > got[i].Mastery {
			t.Fatalf("không sắp xếp tăng dần theo mastery: %+v", got)
		}
	}
}

func TestTopErrorsIsDeterministic(t *testing.T) {
	t.Parallel()
	// Map trong Go duyệt ngẫu nhiên. Thiếu thứ tự phụ ổn định thì hai lần recompute trên
	// cùng dữ liệu sinh ra top_errors khác thứ tự, và UI trông như dữ liệu đang nhảy.
	in := map[string]masteryValue{}
	for _, p := range []string{"θ", "ð", "ʃ", "ʒ", "z", "v", "s"} {
		in[p] = masteryValue{Mastery: 40, Attempts: 10} // cố tình bằng điểm nhau
	}
	first := buildTopErrors(in)
	for i := 0; i < 20; i++ {
		got := buildTopErrors(in)
		for j := range got {
			if got[j].Phoneme != first[j].Phoneme {
				t.Fatalf("thứ tự không ổn định: %v rồi %v", first, got)
			}
		}
	}
}

// ── điểm tổng ─────────────────────────────────────────────────────────────────

func TestOverallScoreCoversAllPhonemesNotJustWeakest(t *testing.T) {
	t.Parallel()
	// Lỗi cũ: điểm tổng tính trên danh sách hiển thị `LIMIT 20` (20 âm yếu nhất), nên
	// người luyện rộng bị chặn trên bởi chính cái đuôi yếu của mình.
	in := map[string]masteryValue{}
	for i := 0; i < 20; i++ {
		in[string(rune('a'+i))] = masteryValue{Mastery: 40, Attempts: 5}
	}
	for i := 0; i < 20; i++ {
		in[string(rune('A'+i))] = masteryValue{Mastery: 90, Attempts: 5}
	}

	got := overallScore(in)
	if got == nil {
		t.Fatal("overallScore = nil trên tập không rỗng")
	}
	if math.Abs(*got-65) > 1e-9 {
		t.Fatalf("overallScore = %v, want 65 — phần đã làm tốt phải được tính", *got)
	}
}

func TestOverallScoreNilWhenNothingPractised(t *testing.T) {
	t.Parallel()
	// Chưa luyện gì KHÔNG phải 0 điểm. Trả 0 sẽ vẽ ra một đường biểu đồ chạm đáy cho người
	// mới, đúng thứ dễ làm họ bỏ app nhất.
	if got := overallScore(map[string]masteryValue{}); got != nil {
		t.Fatalf("overallScore = %v, want nil", *got)
	}
}

// ── phụ âm cuối ───────────────────────────────────────────────────────────────

func TestConsonantSetCoversVietnameseTargets(t *testing.T) {
	t.Parallel()
	// Những âm cuối mà người học hay nuốt. Sót một âm ở đây nghĩa là chỉ số
	// final_consonant im lặng bỏ qua đúng lỗi nó sinh ra để đo.
	for _, p := range []string{"t", "d", "k", "s", "z", "θ", "ð", "ʃ", "ʒ", "tʃ", "dʒ", "l", "ɹ", "n", "ŋ", "v", "f"} {
		if !englishConsonants[p] {
			t.Errorf("thiếu phụ âm %q trong englishConsonants", p)
		}
	}
}

func TestConsonantSetExcludesVowels(t *testing.T) {
	t.Parallel()
	// Tập đóng: ký hiệu lạ hoặc nguyên âm phải KHÔNG được tính là phụ âm cuối.
	for _, p := range []string{"ə", "ɐ", "ᵻ", "ɚ", "iː", "uː", "ɑː", "aɪ", "oʊ", "ʌ", "æ", ""} {
		if englishConsonants[p] {
			t.Errorf("%q bị tính nhầm là phụ âm", p)
		}
	}
}

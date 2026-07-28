package main

import (
	"os"
	"strings"
	"testing"
)

// asciiLookalikes — ký tự bàn phím trông GIỐNG ký hiệu IPA nhưng khác mã.
//
// Đây là kiểu lỗi nguy hiểm nhất trong file nội dung này: gõ `g` thay vì `ɡ` (U+0261),
// `r` thay vì `ɹ` (U+0279), hoặc dấu hai chấm `:` thay vì `ː` (U+02D0). Code biên dịch
// bình thường, seed chạy bình thường, nhưng `GetFixGuide` lọc `phoneme = $1` sẽ KHÔNG BAO
// GIỜ khớp — hướng dẫn im lặng biến mất và không có lỗi nào được ghi ra.
var asciiLookalikes = map[rune]string{
	'g': "ɡ (U+0261, chữ g một tầng của IPA)",
	'r': "ɹ (U+0279, r lộn ngược)",
	':': "ː (U+02D0, dấu kéo dài IPA)",
	'?': "ʔ (U+0294, tắc thanh hầu)",
	'!': "ǃ (U+01C3)",
}

func checkPhonemeSymbol(t *testing.T, where, sym string) {
	t.Helper()
	if sym == "" {
		t.Errorf("%s: ký hiệu âm vị rỗng", where)
		return
	}
	for _, r := range sym {
		if want, bad := asciiLookalikes[r]; bad {
			t.Errorf("%s: ký hiệu %q chứa ký tự ASCII %q — phải dùng %s",
				where, sym, string(r), want)
		}
	}
	if strings.TrimSpace(sym) != sym {
		t.Errorf("%s: ký hiệu %q có khoảng trắng thừa", where, sym)
	}
}

func TestFixGuidePhonemeSymbolsAreRealIPA(t *testing.T) {
	t.Parallel()
	for _, g := range fixGuides {
		checkPhonemeSymbol(t, "fix_guide "+g.phoneme, g.phoneme)
	}
}

func TestMinimalPairPhonemeSymbolsAreRealIPA(t *testing.T) {
	t.Parallel()
	for _, p := range minimalPairs {
		where := p.wordA + "/" + p.wordB
		checkPhonemeSymbol(t, where+" (a)", p.phonemeA)
		checkPhonemeSymbol(t, where+" (b)", p.phonemeB)
	}
}

func TestNoDuplicateFixGuidePhonemes(t *testing.T) {
	t.Parallel()
	// `GetFixGuide` dùng `LIMIT 1` nên hai hướng dẫn cho cùng một âm nghĩa là một cái
	// không bao giờ được nhìn thấy, và cái nào thắng thì tuỳ thứ tự Postgres trả về.
	seen := map[string]bool{}
	for _, g := range fixGuides {
		if seen[g.phoneme] {
			t.Errorf("âm %q có hai hướng dẫn — một cái sẽ không bao giờ hiển thị", g.phoneme)
		}
		seen[g.phoneme] = true
	}
}

func TestEveryMinimalPairPhonemeHasAFixGuide(t *testing.T) {
	t.Parallel()
	// Người học sai một cặp âm rồi bấm "sửa thế nào" phải có nội dung để đọc. Thiếu hướng
	// dẫn thì họ nhận về màn hình fallback rỗng đúng lúc họ đang cần giúp nhất.
	guides := map[string]bool{}
	for _, g := range fixGuides {
		guides[g.phoneme] = true
	}
	for _, p := range minimalPairs {
		for _, sym := range []string{p.phonemeA, p.phonemeB} {
			if !guides[sym] {
				t.Errorf("cặp %s/%s dùng âm %q nhưng chưa có fix_guide",
					p.wordA, p.wordB, sym)
			}
		}
	}
}

func TestEveryContentTagExists(t *testing.T) {
	t.Parallel()
	// Runner seed báo lỗi lúc chạy nếu nhãn không tồn tại, nhưng bắt ở đây thì không phải
	// dựng database mới biết.
	tags := map[string]bool{}
	for _, tag := range l1ErrorTags {
		tags[tag.code] = true
	}
	for _, g := range fixGuides {
		if g.tagCode != "" && !tags[g.tagCode] {
			t.Errorf("fix_guide %q trỏ tới nhãn lỗi %q không tồn tại", g.phoneme, g.tagCode)
		}
	}
	for _, p := range minimalPairs {
		if !tags[p.tagCode] {
			t.Errorf("cặp %s/%s trỏ tới nhãn lỗi %q không tồn tại",
				p.wordA, p.wordB, p.tagCode)
		}
	}
}

func TestMinimalPairsHaveDistinctWords(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, p := range minimalPairs {
		if p.wordA == p.wordB {
			t.Errorf("cặp %q có hai từ giống nhau", p.wordA)
		}
		if p.phonemeA == p.phonemeB {
			t.Errorf("cặp %s/%s có hai âm giống nhau — không phải cặp tối thiểu",
				p.wordA, p.wordB)
		}
		key := p.wordA + "|" + p.wordB
		if seen[key] {
			t.Errorf("cặp %s/%s bị lặp", p.wordA, p.wordB)
		}
		seen[key] = true
	}
}

func TestContentHasSubstance(t *testing.T) {
	t.Parallel()
	// Chặn mục rỗng lọt vào. Hướng dẫn một câu cụt không giúp được ai, và ví dụ rỗng làm
	// màn hình Fix Guide trông như hỏng.
	const minGuideLen = 80
	for _, g := range fixGuides {
		if len([]rune(g.tongueVI)) < minGuideLen {
			t.Errorf("hướng dẫn cho %q quá ngắn (%d ký tự)", g.phoneme, len([]rune(g.tongueVI)))
		}
		if len(g.examples) < 3 {
			t.Errorf("hướng dẫn cho %q chỉ có %d ví dụ, cần ≥3", g.phoneme, len(g.examples))
		}
	}
	for _, p := range minimalPairs {
		if strings.TrimSpace(p.explainVI) == "" {
			t.Errorf("cặp %s/%s thiếu giải thích", p.wordA, p.wordB)
		}
		if p.difficulty < 1 || p.difficulty > 3 {
			t.Errorf("cặp %s/%s có difficulty %d ngoài thang 1–3",
				p.wordA, p.wordB, p.difficulty)
		}
	}
}

func TestFreemiumHasEnoughFreePairs(t *testing.T) {
	t.Parallel()
	// Người dùng miễn phí phải có đủ nội dung để thấy tính năng đáng giá trước khi bị chặn.
	free := 0
	for _, p := range minimalPairs {
		if p.isFree {
			free++
		}
	}
	if free < 10 {
		t.Errorf("chỉ có %d cặp miễn phí — quá ít để người dùng mới thấy giá trị", free)
	}
}

// ── bộ đánh giá ban đầu ───────────────────────────────────────────────────────

func TestOnboardingSetIsMarkedDefault(t *testing.T) {
	t.Parallel()
	// `GetPreAssessment` lọc `is_default` khi client không truyền code. Không bộ nào được
	// đánh dấu thì endpoint trả 404 và onboarding chết ở màn hình đầu tiên.
	//
	// Migration 000009 chỉ UPDATE dòng đã tồn tại, nên trên database MỚI TINH (migration
	// chạy trước seed) nó không đặt được cờ cho ai. Seed phải tự khai.
	//
	// Đọc thẳng mã nguồn vì đây là chuỗi SQL, không phải hàm gọi được từ test.
	src := readSeedMain(t)

	insert := extractOnboardingInsert(t, src)
	if !strings.Contains(insert, "is_default") {
		t.Fatal("câu INSERT bộ onboarding không khai is_default — GetPreAssessment sẽ trả 404 " +
			"trên mọi database mới")
	}
	if !strings.Contains(insert, "TRUE") {
		t.Fatal("is_default có mặt nhưng không đặt TRUE")
	}
}

func TestBenchmarkSetIsNotDefault(t *testing.T) {
	t.Parallel()
	// Chỉ MỘT bộ được là mặc định — có unique index `ON assessment_sets(type) WHERE
	// is_default` cưỡng chế. Bộ benchmark 23 câu mà thành mặc định thì người dùng mới phải
	// đọc 5 phút trong onboarding.
	src := readSeedMain(t)
	if !strings.Contains(src, "'pre_assessment', $2, $3, 'en-US', $4, FALSE") {
		t.Error("bộ benchmark phải khai is_default = FALSE tường minh")
	}
}

func readSeedMain(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("đọc main.go: %v", err)
	}
	return string(b)
}

// extractOnboardingInsert lấy câu INSERT assessment_sets ĐẦU TIÊN — đó là bộ onboarding;
// bộ benchmark nằm sau trong cùng file.
func extractOnboardingInsert(t *testing.T, src string) string {
	t.Helper()
	i := strings.Index(src, "INSERT INTO assessment_sets")
	if i < 0 {
		t.Fatal("không tìm thấy INSERT INTO assessment_sets trong main.go")
	}
	end := strings.Index(src[i:], "RETURNING id")
	if end < 0 {
		t.Fatal("không tìm thấy điểm kết thúc câu INSERT")
	}
	return src[i : i+end]
}

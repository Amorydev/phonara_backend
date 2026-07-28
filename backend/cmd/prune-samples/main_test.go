package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestStorageKeyNormalization(t *testing.T) {
	t.Parallel()
	// Hàm này quyết định file nào bị coi là mồ côi. Chuẩn hoá hụt một dạng nghĩa là file
	// đang sống không khớp tham chiếu nào và bị XOÁ. Mỗi ca dưới đây là một dạng thật sự
	// có trong CSDL hoặc có thể xuất hiện.
	cases := []struct{ in, want string }{
		// Dạng worker tts đang ghi.
		{"/v1/media/sample/assessment/abc.mp3", "sample/assessment/abc.mp3"},
		// Khoá trần — dạng `Store.Put` trả về.
		{"sample/minimal-pair/x_us.mp3", "sample/minimal-pair/x_us.mp3"},
		// Có scheme, do `normalizeRef` bên storage chấp nhận cả hai.
		{"s3://sample/content/1.mp3", "sample/content/1.mp3"},
		{"file://sample/content/1.mp3", "sample/content/1.mp3"},
		// URL tuyệt đối cùng host vẫn phải rút được khoá.
		{"https://api.phonara.online/v1/media/sample/assessment/z.mp3", "sample/assessment/z.mp3"},
		// Dấu gạch chéo thừa.
		{"/sample/assessment/y.mp3", "sample/assessment/y.mp3"},
		{"  sample/assessment/w.mp3  ", "sample/assessment/w.mp3"},
		// Rỗng.
		{"", ""},
		// Host ngoài, KHÔNG phải object của bucket này → bỏ qua, đừng đoán.
		{"https://cdn.example.com/promo.png", ""},
	}
	for _, c := range cases {
		if got := storageKey(c.in); got != c.want {
			t.Errorf("storageKey(%q) = %q, muốn %q", c.in, got, c.want)
		}
	}
}

func TestAmbiguousValuesResolveTowardKeeping(t *testing.T) {
	t.Parallel()
	// Lệnh này XOÁ, nên hai kiểu sai không ngang nhau: nhận nhầm thì giữ lại vài KB rác,
	// bỏ sót thì mất file thật vĩnh viễn. Mọi ca mập mờ phải rơi về phía GIỮ.
	//
	// Ca dưới đây là URL tuyệt đối tới host khác nhưng vẫn chứa `/v1/media/`. Rút khoá ra
	// (thay vì bỏ qua) khiến file tương ứng được coi là đang dùng — đúng hướng an toàn.
	got := storageKey("https://cdn.khac.com/v1/media/sample/assessment/abc.mp3")
	if got != "sample/assessment/abc.mp3" {
		t.Errorf("storageKey = %q, muốn rút được khoá. Bỏ qua giá trị chứa /v1/media/ "+
			"sẽ khiến file đang dùng bị xoá nếu có đường code nào ghi URL tuyệt đối", got)
	}
}

func TestReferenceColumnsHasNoDuplicates(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, c := range referenceColumns {
		k := c.table + "." + c.column
		if seen[k] {
			t.Errorf("%s khai hai lần", k)
		}
		seen[k] = true
	}
}

// excludedColumns là các cột TRÔNG như trỏ tới media nhưng không phải object của bucket
// mẫu. Mỗi mục phải có lý do — đây là danh sách miễn trừ của một lệnh XOÁ.
var excludedColumns = map[string]string{
	// Bản ghi âm người dùng: bucket KHÁC (`S3_BUCKET_RECORDINGS`). Lệnh này không bao giờ
	// quét bucket đó.
	"assessment_jobs.audio_ref":           "bucket bản ghi",
	"exam_sessions.audio_ref":             "bucket bản ghi",
	"practice_item_results.audio_ref":     "bucket bản ghi",
	"account_deletion_requests.audio_ref": "bucket bản ghi",
	// Ảnh đại diện do người dùng đặt, URL ngoài.
	"users.avatar_url": "URL ngoài, không phải bucket mẫu",
	// "key" ở đây là khoá cấu hình dạng chuỗi, không phải khoá lưu trữ.
	"app_configs.key":    "khoá cấu hình",
	"practice_modes.key": "khoá cấu hình",
	// Tên drawable phía client (`ic_word`, `ic_shadowing`), không phải object lưu trữ.
	// Đã kiểm giá trị thật trong CSDL, không suy đoán từ tên cột.
	"practice_modes.icon": "tên drawable của client",
	// `badges.icon_url` cũng vậy: giá trị thật là `ic_badge_streak_3`, không phải đường dẫn.
	// Để nó trong referenceColumns thì mỗi lần chạy đẻ ra 13 dòng "THIẾU FILE" giả, và
	// nhiễu đó sẽ che mất một tham chiếu chết THẬT.
	"badges.icon_url":                       "tên drawable của client",
	"assessment_jobs.idempotency_key":       "khoá idempotency",
	"practice_item_results.idempotency_key": "khoá idempotency",
}

func TestEveryMediaColumnIsClassified(t *testing.T) {
	t.Parallel()
	// Thêm một cột audio mới mà quên khai vào `referenceColumns` thì mọi file nó trỏ tới
	// trở thành "mồ côi" và bị xoá ở lần chạy `-delete` kế tiếp. Test này bắt buộc mỗi cột
	// ứng viên phải được phân loại TƯỜNG MINH: hoặc là tham chiếu, hoặc được miễn trừ có
	// lý do.
	known := map[string]bool{}
	for _, c := range referenceColumns {
		known[c.table+"."+c.column] = true
	}

	for tbl, cols := range candidateColumnsFromMigrations(t) {
		for _, col := range cols {
			k := tbl + "." + col
			if known[k] {
				continue
			}
			if _, ok := excludedColumns[k]; ok {
				continue
			}
			t.Errorf("cột %s trông như trỏ tới media nhưng chưa được phân loại — "+
				"thêm vào referenceColumns (nếu là file trong bucket mẫu) hoặc vào "+
				"excludedColumns kèm lý do. Bỏ qua nó sẽ khiến prune-samples XOÁ file "+
				"đang được dùng.", k)
		}
	}
}

var (
	reCreateTable = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?"?(\w+)"?\s*\((.*?)\n\s*\);`)
	reAddColumn   = regexp.MustCompile(`(?is)ALTER\s+TABLE\s+"?(\w+)"?\s+ADD\s+COLUMN\s+(?:IF\s+NOT\s+EXISTS\s+)?"?(\w+)"?`)
	reMediaName   = regexp.MustCompile(`(?i)(audio|url|media|icon|banner|thumbnail|image|avatar)`)
	reColumnLine  = regexp.MustCompile(`(?m)^\s*"?(\w+)"?\s+(?:TEXT|VARCHAR|CHAR)`)
)

// candidateColumnsFromMigrations đọc migrations thay vì hỏi CSDL, để test chạy được trong
// CI mà không cần Postgres. Đọc file .up.sql là đủ: cột chỉ sinh ra ở đó.
func candidateColumnsFromMigrations(t *testing.T) map[string][]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("không tìm thấy migration nào — test này sẽ luôn xanh một cách vô nghĩa")
	}

	out := map[string][]string{}
	add := func(tbl, col string) {
		if !reMediaName.MatchString(col) {
			return
		}
		for _, existing := range out[tbl] {
			if existing == col {
				return
			}
		}
		out[tbl] = append(out[tbl], col)
	}

	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("đọc %s: %v", p, err)
		}
		sql := string(b)

		for _, m := range reCreateTable.FindAllStringSubmatch(sql, -1) {
			tbl, body := m[1], m[2]
			for _, cm := range reColumnLine.FindAllStringSubmatch(body, -1) {
				add(tbl, strings.ToLower(cm[1]))
			}
		}
		for _, m := range reAddColumn.FindAllStringSubmatch(sql, -1) {
			add(m[1], strings.ToLower(m[2]))
		}
	}
	return out
}

package worker

import "testing"

// mediaURL phải cho ra đường dẫn client parse được.
//
// Bug đã gặp: quên bỏ tiền tố scheme của storage nên sinh ra
// "/v1/media/file://sample/..." — ExoPlayer không parse được và im lặng, đúng loại lỗi
// chỉ lộ ra khi chạy thật trên máy.
func TestMediaURLStripsStorageScheme(t *testing.T) {
	cases := map[string]string{
		"file://sample/assessment/abc.mp3": "/v1/media/sample/assessment/abc.mp3",
		"sample/content/xyz.mp3":           "/v1/media/sample/content/xyz.mp3",
	}
	for ref, want := range cases {
		if got := mediaURL(ref); got != want {
			t.Errorf("mediaURL(%q) = %q, muốn %q", ref, got, want)
		}
	}
}

func TestMediaURLNeverContainsScheme(t *testing.T) {
	got := mediaURL("file://sample/assessment/abc.mp3")
	for _, bad := range []string{"file:", "://"} {
		if contains(got, bad) {
			t.Errorf("URL %q còn chứa %q — client sẽ không parse được", got, bad)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

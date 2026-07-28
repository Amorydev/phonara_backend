package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Tiền tố khoá là RANH GIỚI BẢO MẬT, không phải quy ước đặt tên.
//
// Audio mẫu công khai được; bản ghi giọng người dùng thì không. Nếu bucketFor rơi về một
// mặc định khi gặp tiền tố lạ, một đường code mới sẽ âm thầm ghi bản ghi riêng tư vào
// bucket công khai — đúng loại lỗi đã từng để lọt ở endpoint media.
func TestBucketForRejectsUnknownPrefix(t *testing.T) {
	s := &S3Store{sampleBucket: "samples", recordingBucket: "recordings"}

	ok := map[string]string{
		"sample/assessment/a.mp3":      "samples",
		"sample/content/b-uk.mp3":      "samples",
		"audio/user-1/2026/07/job.wav": "recordings",
	}
	for key, want := range ok {
		got, err := s.bucketFor(key)
		if err != nil {
			t.Errorf("bucketFor(%q) lỗi bất ngờ: %v", key, err)
			continue
		}
		if got != want {
			t.Errorf("bucketFor(%q) = %q, muốn %q", key, got, want)
		}
	}

	rejected := []string{
		"",
		"samples/x.mp3", // gần giống nhưng KHÔNG phải "sample/"
		"audios/x.wav",  // gần giống nhưng KHÔNG phải "audio/"
		"tmp/x.mp3",
		"../escape.mp3",
		"sample", // thiếu dấu gạch
	}
	for _, key := range rejected {
		if _, err := s.bucketFor(key); err == nil {
			t.Errorf("bucketFor(%q) phải lỗi — tiền tố lạ không được rơi về mặc định", key)
		}
	}
}

// Ref cũ do LocalStore tạo mang tiền tố "file://". Chấp nhận cả hai dạng để đổi backend
// lưu trữ không cần migrate CSDL.
func TestNormalizeRefStripsLegacyScheme(t *testing.T) {
	cases := map[string]string{
		"file://sample/a.mp3": "sample/a.mp3",
		"s3://sample/a.mp3":   "sample/a.mp3",
		"sample/a.mp3":        "sample/a.mp3",
	}
	for in, want := range cases {
		if got := normalizeRef(in); got != want {
			t.Errorf("normalizeRef(%q) = %q, muốn %q", in, got, want)
		}
	}
}

// Key() phải sinh tiền tố khớp PrefixRecording, nếu không S3Store từ chối ghi bản ghi
// người dùng và mọi lượt chấm hỏng ngay.
func TestKeyUsesRecordingPrefix(t *testing.T) {
	k := Key("user-123", "job-456")
	if len(k) < len(PrefixRecording) || k[:len(PrefixRecording)] != PrefixRecording {
		t.Fatalf("Key() = %q, phải bắt đầu bằng %q", k, PrefixRecording)
	}
}

func TestLocalStoreDeletePrefix(t *testing.T) {
	t.Parallel()
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	userPrefix := PrefixRecording + "user-123/"
	ref, err := store.Put(ctx, userPrefix+"2026/07/job.wav", []byte("audio"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePrefix(ctx, userPrefix); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after DeletePrefix error = %v, want ErrNotFound", err)
	}

	samplePath := filepath.Join(store.root, "sample", "keep.mp3")
	if err := os.MkdirAll(filepath.Dir(samplePath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(samplePath, []byte("sample"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePrefix(ctx, PrefixSample); err == nil {
		t.Fatal("sample prefix deletion must be rejected")
	}
	if err := store.DeletePrefix(ctx, PrefixRecording); err == nil {
		t.Fatal("global recording prefix deletion must be rejected")
	}
	if _, err := os.Stat(samplePath); err != nil {
		t.Fatalf("sample file was modified: %v", err)
	}
}

// Package storage lưu trữ audio người dùng ghi.
//
// Interface tách khỏi cài đặt vì hai lý do:
//   - production dùng S3/MinIO, test dùng thư mục tạm, không cần MinIO chạy nền
//   - cài đặt S3 chưa viết (cần thêm dependency); đổi sau không đụng caller
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotFound khi audio_ref không tồn tại.
var ErrNotFound = errors.New("audio not found")

// Store lưu và đọc lại audio theo một tham chiếu dạng chuỗi (`audio_ref`).
//
// audio_ref được lưu vào DB, nên định dạng của nó là một phần hợp đồng dữ liệu — đổi
// cách sinh ref sẽ làm mọi bản ghi cũ không đọc được. Giữ ổn định.
type Store interface {
	Put(ctx context.Context, key string, data []byte) (audioRef string, err error)
	Get(ctx context.Context, audioRef string) ([]byte, error)
	Delete(ctx context.Context, audioRef string) error
	DeletePrefix(ctx context.Context, prefix string) error
}

// Key sinh khoá lưu trữ theo user và thời gian.
//
// Có userID trong đường dẫn để xoá toàn bộ audio của một user (yêu cầu quyền riêng tư,
// TypeAccountDelete) chỉ là xoá một tiền tố, không phải quét toàn bộ bucket.
func Key(userID, jobID string) string {
	now := time.Now().UTC()
	return fmt.Sprintf("audio/%s/%04d/%02d/%s.wav", userID, now.Year(), now.Month(), jobID)
}

// LocalStore lưu xuống đĩa. Dùng cho dev và test.
//
// KHÔNG dùng cho production nhiều instance: mỗi instance có đĩa riêng nên worker ở máy
// khác sẽ không đọc được audio do API ở máy này ghi.
type LocalStore struct {
	root string
}

// NewLocalStore tạo store trên thư mục root.
func NewLocalStore(root string) (*LocalStore, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	return &LocalStore{root: root}, nil
}

func (s *LocalStore) path(audioRef string) (string, error) {
	// audio_ref đến từ DB nhưng vẫn phải kiểm — một ref chứa ".." sẽ ghi/đọc ra ngoài
	// thư mục gốc. Không tin dữ liệu chỉ vì nó đã nằm trong DB của mình.
	clean := filepath.Clean("/" + strings.TrimPrefix(audioRef, "file://"))
	full := filepath.Join(s.root, clean)
	if !strings.HasPrefix(full, filepath.Clean(s.root)+string(os.PathSeparator)) {
		return "", fmt.Errorf("audio_ref thoát khỏi thư mục gốc: %q", audioRef)
	}
	return full, nil
}

// Put ghi audio và trả về audio_ref.
//
// Ref là khoá trần, KHÔNG kèm scheme — giống S3Store. Nhúng chi tiết cài đặt vào ref sẽ
// khiến đổi backend lưu trữ phải migrate cả bản ghi cũ trong CSDL.
func (s *LocalStore) Put(_ context.Context, key string, data []byte) (string, error) {
	ref := key
	full, err := s.path(ref)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return "", fmt.Errorf("create audio dir: %w", err)
	}
	if err := os.WriteFile(full, data, 0o640); err != nil {
		return "", fmt.Errorf("write audio: %w", err)
	}
	return ref, nil
}

// Get đọc audio theo ref.
func (s *LocalStore) Get(_ context.Context, audioRef string) ([]byte, error) {
	full, err := s.path(audioRef)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(full)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read audio: %w", err)
	}
	return data, nil
}

// Delete xoá audio. Xoá thứ không tồn tại không phải lỗi — thao tác idempotent.
func (s *LocalStore) Delete(_ context.Context, audioRef string) error {
	full, err := s.path(audioRef)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete audio: %w", err)
	}
	return nil
}

// DeletePrefix removes a user's recording subtree. Sample audio is excluded
// from this privacy operation by construction.
func (s *LocalStore) DeletePrefix(_ context.Context, prefix string) error {
	prefix = normalizeRef(prefix)
	if !isUserRecordingPrefix(prefix) {
		return fmt.Errorf("refuse to delete unsafe recording prefix %q", prefix)
	}
	full, err := s.path(prefix)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(full); err != nil {
		return fmt.Errorf("delete audio prefix: %w", err)
	}
	return nil
}

func isUserRecordingPrefix(prefix string) bool {
	if !strings.HasPrefix(prefix, PrefixRecording) || !strings.HasSuffix(prefix, "/") {
		return false
	}
	userPart := strings.TrimSuffix(strings.TrimPrefix(prefix, PrefixRecording), "/")
	return userPart != "" && userPart != "." && userPart != ".." &&
		!strings.ContainsAny(userPart, `/\`)
}

// ReadAllLimit đọc tối đa max byte, chặn upload quá lớn làm cạn bộ nhớ.
//
// Đọc thêm 1 byte để phân biệt "vừa đúng giới hạn" với "vượt giới hạn" — nếu chỉ đọc
// đúng max thì file dài hơn sẽ bị cắt cụt âm thầm thành audio hỏng.
func ReadAllLimit(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("audio vượt %d byte", max)
	}
	return data, nil
}

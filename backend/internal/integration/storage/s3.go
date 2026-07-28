package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Tiền tố khoá quyết định audio thuộc loại nào — và do đó nằm ở bucket nào.
//
// Đây không phải quy ước đặt tên cho đẹp: nó là **ranh giới bảo mật**. Audio mẫu công
// khai được, bản ghi giọng người dùng thì không. Tách hai bucket để phân quyền của hạ
// tầng cưỡng chế điều đó, thay vì phụ thuộc vào việc mọi lập trình viên nhớ kiểm tiền tố
// ở mọi endpoint — cách phụ thuộc trí nhớ ấy đã từng để lọt một lỗ hổng.
const (
	PrefixSample    = "sample/"
	PrefixRecording = "audio/"
)

// S3Store lưu trên S3 hoặc MinIO.
//
// Thay LocalStore ở production vì LocalStore chỉ chạy được khi api và worker chung một
// đĩa. Thêm một API server thứ hai là worker không tìm thấy file người dùng vừa upload.
type S3Store struct {
	client          *minio.Client
	sampleBucket    string
	recordingBucket string
}

// S3Config là tham số kết nối.
type S3Config struct {
	Endpoint        string
	AccessKey       string
	SecretKey       string
	Region          string
	UseSSL          bool
	SampleBucket    string
	RecordingBucket string
}

// NewS3Store kết nối và đảm bảo hai bucket tồn tại.
func NewS3Store(ctx context.Context, cfg S3Config) (*S3Store, error) {
	endpoint := strings.TrimPrefix(strings.TrimPrefix(cfg.Endpoint, "https://"), "http://")
	useSSL := cfg.UseSSL || strings.HasPrefix(cfg.Endpoint, "https://")

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: useSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("khởi tạo client S3: %w", err)
	}

	s := &S3Store{
		client:          client,
		sampleBucket:    cfg.SampleBucket,
		recordingBucket: cfg.RecordingBucket,
	}
	for _, bucket := range []string{s.sampleBucket, s.recordingBucket} {
		if err := s.ensureBucket(ctx, bucket, cfg.Region); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *S3Store) ensureBucket(ctx context.Context, bucket, region string) error {
	exists, err := s.client.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("kiểm bucket %s: %w", bucket, err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: region}); err != nil {
		return fmt.Errorf("tạo bucket %s: %w", bucket, err)
	}
	slog.Info("đã tạo bucket", "bucket", bucket)
	return nil
}

// bucketFor chọn bucket theo tiền tố khoá.
//
// Tiền tố lạ là LỖI, không phải rơi về mặc định: một đường code mới quên quy ước sẽ hỏng
// ngay lúc chạy thay vì âm thầm ghi bản ghi riêng tư vào bucket công khai.
func (s *S3Store) bucketFor(key string) (string, error) {
	switch {
	case strings.HasPrefix(key, PrefixSample):
		return s.sampleBucket, nil
	case strings.HasPrefix(key, PrefixRecording):
		return s.recordingBucket, nil
	default:
		return "", fmt.Errorf(
			"khoá %q không có tiền tố hợp lệ (%q hoặc %q) — không xác định được bucket",
			key, PrefixSample, PrefixRecording)
	}
}

// Put ghi object và trả về audio_ref.
//
// Ref là khoá trần, KHÔNG kèm scheme: khoá đó đi vào CSDL, và nhúng chi tiết cài đặt
// ("file://", "s3://") vào dữ liệu sẽ khiến đổi backend lưu trữ phải sửa cả bản ghi cũ.
func (s *S3Store) Put(ctx context.Context, key string, data []byte) (string, error) {
	bucket, err := s.bucketFor(key)
	if err != nil {
		return "", err
	}
	_, err = s.client.PutObject(ctx, bucket, key,
		bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentTypeOf(key)},
	)
	if err != nil {
		return "", fmt.Errorf("ghi object %s/%s: %w", bucket, key, err)
	}
	return key, nil
}

// Get đọc object theo ref.
func (s *S3Store) Get(ctx context.Context, audioRef string) ([]byte, error) {
	key := normalizeRef(audioRef)
	bucket, err := s.bucketFor(key)
	if err != nil {
		return nil, err
	}

	obj, err := s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("mở object %s/%s: %w", bucket, key, err)
	}
	defer obj.Close() //nolint:errcheck

	data, err := io.ReadAll(obj)
	if err != nil {
		// minio-go chỉ báo lỗi khi ĐỌC chứ không khi GetObject, nên phải kiểm ở đây.
		var resp minio.ErrorResponse
		if errors.As(err, &resp) && resp.Code == "NoSuchKey" {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("đọc object %s/%s: %w", bucket, key, err)
	}
	return data, nil
}

// Delete xoá object. Xoá thứ không tồn tại không phải lỗi — thao tác idempotent.
func (s *S3Store) Delete(ctx context.Context, audioRef string) error {
	key := normalizeRef(audioRef)
	bucket, err := s.bucketFor(key)
	if err != nil {
		return err
	}
	if err := s.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("xoá object %s/%s: %w", bucket, key, err)
	}
	return nil
}

// DeletePrefix removes all objects under a private, user-scoped recording prefix.
func (s *S3Store) DeletePrefix(ctx context.Context, prefix string) error {
	prefix = normalizeRef(prefix)
	if !isUserRecordingPrefix(prefix) {
		return fmt.Errorf("từ chối xoá tiền tố bản ghi không an toàn: %q", prefix)
	}
	bucket, err := s.bucketFor(prefix)
	if err != nil {
		return err
	}
	for object := range s.client.ListObjects(ctx, bucket, minio.ListObjectsOptions{
		Prefix: prefix, Recursive: true,
	}) {
		if object.Err != nil {
			return fmt.Errorf("liệt kê object dưới %s: %w", prefix, object.Err)
		}
		if err := s.client.RemoveObject(
			ctx, bucket, object.Key, minio.RemoveObjectOptions{},
		); err != nil {
			return fmt.Errorf("xoá object %s/%s: %w", bucket, object.Key, err)
		}
	}
	return nil
}

// normalizeRef bỏ scheme của dữ liệu cũ.
//
// Bản ghi tạo bởi LocalStore mang tiền tố "file://". Chấp nhận cả hai dạng để không phải
// migrate CSDL chỉ vì đổi backend lưu trữ.
func normalizeRef(audioRef string) string {
	return strings.TrimPrefix(strings.TrimPrefix(audioRef, "file://"), "s3://")
}

func contentTypeOf(key string) string {
	switch {
	case strings.HasSuffix(key, ".mp3"):
		return "audio/mpeg"
	case strings.HasSuffix(key, ".wav"):
		return "audio/wav"
	case strings.HasSuffix(key, ".ogg"):
		return "audio/ogg"
	default:
		return "application/octet-stream"
	}
}

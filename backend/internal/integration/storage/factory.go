package storage

import (
	"context"
	"fmt"
	"log/slog"
)

// FactoryConfig gom mọi tham số cần để chọn và dựng store.
type FactoryConfig struct {
	Driver          string
	LocalRoot       string
	Endpoint        string
	AccessKey       string
	SecretKey       string
	Region          string
	SampleBucket    string
	RecordingBucket string
}

// New dựng store theo driver cấu hình.
//
// Mặc định là "s3": production nhiều instance thì LocalStore hỏng âm thầm (api ghi ở máy
// A, worker đọc ở máy B). Chọn sai mặc định thì lỗi chỉ lộ khi scale, tức đúng lúc tệ nhất.
// Ai cần local thì phải khai báo tường minh.
func New(ctx context.Context, cfg FactoryConfig) (Store, error) {
	switch cfg.Driver {
	case "local":
		slog.Warn("dùng LocalStore — chỉ hợp môi trường một instance",
			"root", cfg.LocalRoot)
		return NewLocalStore(cfg.LocalRoot)
	case "s3", "":
		store, err := NewS3Store(ctx, S3Config{
			Endpoint:        cfg.Endpoint,
			AccessKey:       cfg.AccessKey,
			SecretKey:       cfg.SecretKey,
			Region:          cfg.Region,
			SampleBucket:    cfg.SampleBucket,
			RecordingBucket: cfg.RecordingBucket,
		})
		if err != nil {
			return nil, err
		}
		slog.Info("dùng S3Store",
			"endpoint", cfg.Endpoint,
			"bucket_mẫu", cfg.SampleBucket,
			"bucket_bản_ghi", cfg.RecordingBucket)
		return store, nil
	default:
		return nil, fmt.Errorf("STORAGE_DRIVER không hợp lệ: %q (dùng \"s3\" hoặc \"local\")", cfg.Driver)
	}
}

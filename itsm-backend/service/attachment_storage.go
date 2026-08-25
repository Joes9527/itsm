package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// AttachmentStorage 附件存储抽象：隔离本地文件系统与 MinIO 对象存储。
// key 是 object key（如 "tickets/23_xxx.pdf"），各存储后端自行映射到实际位置。
type AttachmentStorage interface {
	Save(ctx context.Context, key string, reader io.Reader, size int64) error
	Open(ctx context.Context, key string) (io.ReadCloser, int64, error)
	Delete(ctx context.Context, key string) error
}

// LocalAttachmentStorage 本地文件系统存储（rootDir 为根目录，如 "uploads"）。
type LocalAttachmentStorage struct {
	rootDir string
}

// NewLocalAttachmentStorage 创建本地附件存储。
func NewLocalAttachmentStorage(rootDir string) *LocalAttachmentStorage {
	return &LocalAttachmentStorage{rootDir: rootDir}
}

func (s *LocalAttachmentStorage) Save(_ context.Context, key string, reader io.Reader, _ int64) error {
	path := filepath.Join(s.rootDir, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	dst, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, reader); err != nil {
		return fmt.Errorf("copy file: %w", err)
	}
	return nil
}

func (s *LocalAttachmentStorage) Open(_ context.Context, key string) (io.ReadCloser, int64, error) {
	f, err := os.Open(filepath.Join(s.rootDir, key))
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

func (s *LocalAttachmentStorage) Delete(_ context.Context, key string) error {
	return os.Remove(filepath.Join(s.rootDir, key))
}

// MinioAttachmentStorage MinIO 对象存储。
type MinioAttachmentStorage struct {
	client *minio.Client
	bucket string
}

// NewMinioAttachmentStorage 创建 MinIO 附件存储，并确保 bucket 存在。
func NewMinioAttachmentStorage(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*MinioAttachmentStorage, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}
	ctx := context.Background()
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("create bucket: %w", err)
		}
	}
	return &MinioAttachmentStorage{client: client, bucket: bucket}, nil
}

func (s *MinioAttachmentStorage) Save(ctx context.Context, key string, reader io.Reader, size int64) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, reader, size, minio.PutObjectOptions{})
	return err
}

func (s *MinioAttachmentStorage) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	st, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, 0, err
	}
	return obj, st.Size, nil
}

func (s *MinioAttachmentStorage) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

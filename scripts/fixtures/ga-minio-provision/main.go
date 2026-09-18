// Disposable GA storage provisioning only; never linked into application runtime.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func provisionBucket(ctx context.Context, endpoint, accessKey, secretKey, bucket string) error {
	if endpoint == "" || accessKey == "" || secretKey == "" || bucket == "" {
		return fmt.Errorf("explicit isolated storage configuration required")
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: false, // Same private non-TLS MinIO as the generated GA runtime config.
		Region: "us-east-1",
	})
	if err != nil {
		return fmt.Errorf("configure isolated storage: %w", err)
	}
	if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: "us-east-1"}); err != nil {
		return fmt.Errorf("create configured attachment bucket: %w", err)
	}
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("verify configured attachment bucket: %w", err)
	}
	if !exists {
		return fmt.Errorf("configured attachment bucket absent after provisioning")
	}
	return nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := provisionBucket(ctx, os.Getenv("MINIO_ENDPOINT"), os.Getenv("MINIO_ROOT_USER"), os.Getenv("MINIO_ROOT_PASSWORD"), os.Getenv("MINIO_BUCKET")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

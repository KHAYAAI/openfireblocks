package backup

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3ObjectStore is an ObjectStore on S3 or anything that speaks its API
// (MinIO, Ceph, R2). Credentials come from the standard AWS chain: the
// environment, a shared profile, or a role bound to the pod. None are ever
// written to configuration this service owns.
type S3ObjectStore struct {
	client   *s3.Client
	uploader *manager.Uploader
	bucket   string
}

// S3Config says where. Endpoint is empty for AWS itself.
type S3Config struct {
	Bucket   string
	Region   string
	Endpoint string
	// Server-side encryption is on top of the client-side encryption this
	// package always does; "" leaves it to the bucket's default.
	KMSKeyID string
}

func NewS3ObjectStore(ctx context.Context, c S3Config) (*S3ObjectStore, error) {
	if c.Bucket == "" {
		return nil, fmt.Errorf("an off-site bucket is required")
	}
	opts := []func(*config.LoadOptions) error{}
	if c.Region != "" {
		opts = append(opts, config.WithRegion(c.Region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
			o.UsePathStyle = true // MinIO and friends address buckets by path
		}
	})
	return &S3ObjectStore{client: client, uploader: manager.NewUploader(client), bucket: c.Bucket}, nil
}

func (s *S3ObjectStore) Put(ctx context.Context, key string, body io.Reader) error {
	in := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: body}
	in.ServerSideEncryption = types.ServerSideEncryptionAes256
	_, err := s.uploader.Upload(ctx, in)
	return err
}

func (s *S3ObjectStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

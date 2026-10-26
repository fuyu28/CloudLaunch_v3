// Package objstore は、S3 互換のオブジェクトストアにキーを指定して読み書きする。キーの形はコアが決め、ここでは扱わない。
package objstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
}

// S3 は複数の goroutine から同時に呼んでよい。
type S3 struct {
	client *s3.Client
	bucket string
}

func New(cfg Config) (*S3, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("objstore: endpoint and bucket are required")
	}
	region := cfg.Region
	if region == "" {
		region = "auto"
	}
	client := s3.New(s3.Options{
		BaseEndpoint: aws.String(cfg.Endpoint),
		Region:       region,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		// R2 もテスト用のサーバーもパス形式に対応しているので、仮想ホスト形式の DNS に頼らない
		UsePathStyle: true,
		// SDK の既定の CRC32 チェックサムは S3 互換サーバーで拒否されることがあり、内容の検証はコアが sha256 で行うので付けない
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &S3{client: client, bucket: cfg.Bucket}, nil
}

// Get はキーがなければ fs.ErrNotExist で包んだエラーを返す。
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, fmt.Errorf("get %q: %w: %w", key, fs.ErrNotExist, err)
		}
		return nil, fmt.Errorf("get %q: %w", key, err)
	}
	return out.Body, nil
}

func (s *S3) Put(ctx context.Context, key string, body io.ReadSeeker, size int64) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &s.bucket,
		Key:           &key,
		Body:          body,
		ContentLength: aws.Int64(size),
	})
	if err != nil {
		return fmt.Errorf("put %q: %w", key, err)
	}
	return nil
}

// List は prefix で始まるキーを、すべて辞書順で返す。
func (s *S3) List(ctx context.Context, prefix string) ([]string, error) {
	keys := []string{}
	pages := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: &s.bucket, Prefix: &prefix})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list %q: %w", prefix, err)
		}
		for _, o := range page.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
	}
	// S3 は辞書順で返すと定めているが、S3 互換サーバーの実装に順序を委ねず、契約としてここで揃える
	sort.Strings(keys)
	return keys, nil
}

// Delete はキーがなくてもエラーにしない。
func (s *S3) Delete(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key}); err != nil {
		return fmt.Errorf("delete %q: %w", key, err)
	}
	return nil
}

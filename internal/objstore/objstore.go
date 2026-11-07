// Package objstore は、S3 互換のオブジェクトストアにキーを指定して読み書きする。キーの形はコアが決め、ここでは扱わない。
package objstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
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

// deleteBatch は、S3 の DeleteObjects が 1 回に受け付けるキーの上限。
const deleteBatch = 1000

// DeleteMany は、キーがなくてもエラーにしない。途中で失敗したときは、それまでに消したキーは消えたままになる。
// DeleteObjects は、一部のキーを消せなくても HTTP では成功を返し、消せなかったキーをレスポンスの Errors に入れるので、
// それもエラーにする。
func (s *S3) DeleteMany(ctx context.Context, keys []string) error {
	for chunk := range slices.Chunk(keys, deleteBatch) {
		objects := make([]types.ObjectIdentifier, len(chunk))
		for i, key := range chunk {
			objects[i] = types.ObjectIdentifier{Key: aws.String(key)}
		}
		out, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: &s.bucket,
			Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return fmt.Errorf("delete %d keys: %w", len(chunk), err)
		}
		if len(out.Errors) > 0 {
			e := out.Errors[0]
			return fmt.Errorf("delete %q: %s: %s (%d of %d keys failed)",
				aws.ToString(e.Key), aws.ToString(e.Code), aws.ToString(e.Message), len(out.Errors), len(chunk))
		}
	}
	return nil
}

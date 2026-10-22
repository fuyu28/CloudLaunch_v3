// Package objstore は、S3 互換のオブジェクトストア（本番は R2）にキーを指定して読み書きする。キーの形はコアが決め、ここでは扱わない。
package objstore

import (
	"context"
	"errors"
	"io"
)

type Config struct {
	Endpoint        string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
}

// S3 は複数の goroutine から同時に呼んでよい。
type S3 struct{}

func New(cfg Config) (*S3, error) {
	return nil, errors.New("not implemented")
}

// Get はキーがなければ fs.ErrNotExist で包んだエラーを返す。
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (s *S3) Put(ctx context.Context, key string, body io.ReadSeeker, size int64) error {
	return errors.New("not implemented")
}

// List は prefix で始まるキーを、すべて辞書順で返す。
func (s *S3) List(ctx context.Context, prefix string) ([]string, error) {
	return nil, errors.New("not implemented")
}

// Delete はキーがなくてもエラーにしない。
func (s *S3) Delete(ctx context.Context, key string) error {
	return errors.New("not implemented")
}

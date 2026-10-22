// Package memstore は、テストで使うメモリ上のオブジェクトストア。objstore と同じ契約テストを通し、操作ごとに失敗を仕込める。
package memstore

import (
	"context"
	"errors"
	"io"
)

// Op は Before に渡す操作の種類。
type Op int

const (
	OpGet Op = iota
	OpPut
	OpList
	OpDelete
)

type Store struct {
	// Before は各操作の前に呼ばれる。エラーを返すと、その操作は実行されずにそのエラーで失敗する。List では key に prefix が渡る
	Before func(op Op, key string) error
}

func New() *Store {
	return &Store{}
}

func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (s *Store) Put(ctx context.Context, key string, body io.ReadSeeker, size int64) error {
	return errors.New("not implemented")
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	return nil, errors.New("not implemented")
}

func (s *Store) Delete(ctx context.Context, key string) error {
	return errors.New("not implemented")
}

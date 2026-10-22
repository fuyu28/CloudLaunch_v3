// Package memstore は、テストで使うメモリ上のオブジェクトストア。objstore と同じ契約テストを通し、操作ごとに失敗を仕込める。
package memstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"sync"
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

	mu      sync.Mutex
	objects map[string][]byte
}

func New() *Store {
	return &Store{objects: map[string][]byte{}}
}

func (s *Store) before(op Op, key string) error {
	if s.Before == nil {
		return nil
	}
	return s.Before(op, key)
}

func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := s.before(OpGet, key); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("get %q: %w", key, fs.ErrNotExist)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// Put は size と本文の長さが違えば失敗する。S3 も Content-Length と本文がずれたリクエストを拒否するため、それに合わせる
func (s *Store) Put(ctx context.Context, key string, body io.ReadSeeker, size int64) error {
	if err := s.before(OpPut, key); err != nil {
		return err
	}
	b, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("put %q: %w", key, err)
	}
	if int64(len(b)) != size {
		return fmt.Errorf("put %q: body is %d bytes, size is %d", key, len(b), size)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = b
	return nil
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	if err := s.before(OpList, prefix); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := []string{}
	for k := range s.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.before(OpDelete, key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

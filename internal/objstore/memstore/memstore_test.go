package memstore_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/objstoretest"
)

func TestContract(t *testing.T) {
	objstoretest.Run(t, memstore.New())
}

func TestBefore(t *testing.T) {
	t.Run("memstore/Before がエラーを返すと操作は実行されずに失敗する", func(t *testing.T) {
		ctx := context.Background()
		s := memstore.New()
		injected := errors.New("injected")
		s.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpPut && key == "games/x/HEAD" {
				return injected
			}
			return nil
		}

		body := []byte("gen")
		if err := s.Put(ctx, "games/x/HEAD", bytes.NewReader(body), int64(len(body))); !errors.Is(err, injected) {
			t.Fatalf("Put err = %v, want injected", err)
		}
		if _, err := s.Get(ctx, "games/x/HEAD"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Get err = %v, want fs.ErrNotExist (Put must not have run)", err)
		}
		if err := s.Put(ctx, "games/x/objects/a", bytes.NewReader(body), int64(len(body))); err != nil {
			t.Errorf("Put other key: %v", err)
		}
	})
}

// Package objstoretest は、オブジェクトストアの実装（objstore と memstore）に同じ振る舞いを求める契約テストを提供する。
package objstoretest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Store は契約テストが使う操作。使う側のインターフェースは syncore と metasync が別に定義する。
type Store interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Put(ctx context.Context, key string, body io.ReadSeeker, size int64) error
	List(ctx context.Context, prefix string) ([]string, error)
	Delete(ctx context.Context, key string) error
}

// Run は store に契約テストを走らせる。本物のサーバーでは他の実行とキーがぶつからないよう、ケースごとにランダムな接頭辞の下で操作する。
func Run(t *testing.T, store Store) {
	t.Helper()

	t.Run("objstore/Put したものを Get で読める", func(t *testing.T) {
		p := prefix(t, store)
		want := []byte("save data \x00\xff")
		put(t, store, p+"a", want)

		if d := cmp.Diff(want, get(t, store, p+"a")); d != "" {
			t.Errorf("body (-want +got):\n%s", d)
		}
	})

	t.Run("objstore/存在しないキーの Get は fs.ErrNotExist", func(t *testing.T) {
		p := prefix(t, store)
		_, err := store.Get(context.Background(), p+"missing")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run("objstore/同じキーへの Put は上書きになる", func(t *testing.T) {
		p := prefix(t, store)
		put(t, store, p+"a", []byte("old"))
		put(t, store, p+"a", []byte("new"))

		if got := string(get(t, store, p+"a")); got != "new" {
			t.Errorf("body = %q, want %q", got, "new")
		}
	})

	t.Run("objstore/List は prefix で始まるキーだけを辞書順で返す", func(t *testing.T) {
		p := prefix(t, store)
		for _, k := range []string{"dir/b", "dir/a", "dir/sub/c", "dirx", "other"} {
			put(t, store, p+k, []byte(k))
		}

		got := list(t, store, p+"dir/")
		want := []string{p + "dir/a", p + "dir/b", p + "dir/sub/c"}
		if d := cmp.Diff(want, got); d != "" {
			t.Errorf("keys (-want +got):\n%s", d)
		}
	})

	t.Run("objstore/List は 1000 件を超えても全件を返す", func(t *testing.T) {
		p := prefix(t, store)
		const n = 1001
		want := make([]string, 0, n)
		for i := range n {
			k := fmt.Sprintf("%s%04d", p, i)
			put(t, store, k, nil)
			want = append(want, k)
		}

		if d := cmp.Diff(want, list(t, store, p)); d != "" {
			t.Errorf("keys (-want +got):\n%s", d)
		}
	})

	t.Run("objstore/Delete したキーの Get は fs.ErrNotExist", func(t *testing.T) {
		ctx, p := context.Background(), prefix(t, store)
		put(t, store, p+"a", []byte("x"))
		if err := store.Delete(ctx, p+"a"); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		if _, err := store.Get(ctx, p+"a"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run("objstore/存在しないキーの Delete はエラーにならない", func(t *testing.T) {
		p := prefix(t, store)
		if err := store.Delete(context.Background(), p+"missing"); err != nil {
			t.Errorf("Delete: %v", err)
		}
	})

	t.Run("objstore/複数の goroutine から同時に Put と Get をしても壊れない", func(t *testing.T) {
		p := prefix(t, store)
		const workers, perWorker = 8, 10
		var wg sync.WaitGroup
		errs := make(chan error, workers*perWorker)
		for w := range workers {
			wg.Go(func() {
				for i := range perWorker {
					k := fmt.Sprintf("%s%d-%d", p, w, i)
					body := []byte(k)
					if err := store.Put(context.Background(), k, bytes.NewReader(body), int64(len(body))); err != nil {
						errs <- err
						return
					}
					got, err := read(store, k)
					if err != nil {
						errs <- err
						return
					}
					if !bytes.Equal(got, body) {
						errs <- fmt.Errorf("%s: body = %q", k, got)
						return
					}
				}
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}

		keys := list(t, store, p)
		if len(keys) != workers*perWorker {
			t.Errorf("len(keys) = %d, want %d", len(keys), workers*perWorker)
		}
		if !sort.StringsAreSorted(keys) {
			t.Errorf("keys are not sorted")
		}
	})
}

// prefix はケースごとのランダムな接頭辞を返し、ケースの終わりにその下のキーを消す。
func prefix(t *testing.T, store Store) string {
	t.Helper()
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	p := "objstoretest/" + hex.EncodeToString(b) + "/"
	t.Cleanup(func() {
		ctx := context.Background()
		keys, err := store.List(ctx, p)
		if err != nil {
			t.Logf("cleanup list: %v", err)
			return
		}
		for _, k := range keys {
			if err := store.Delete(ctx, k); err != nil {
				t.Logf("cleanup delete %s: %v", k, err)
			}
		}
	})
	return p
}

func put(t *testing.T, store Store, key string, body []byte) {
	t.Helper()
	if err := store.Put(context.Background(), key, bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("Put %s: %v", key, err)
	}
}

func get(t *testing.T, store Store, key string) []byte {
	t.Helper()
	b, err := read(store, key)
	if err != nil {
		t.Fatalf("Get %s: %v", key, err)
	}
	return b
}

func read(store Store, key string) ([]byte, error) {
	rc, err := store.Get(context.Background(), key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func list(t *testing.T, store Store, p string) []string {
	t.Helper()
	keys, err := store.List(context.Background(), p)
	if err != nil {
		t.Fatalf("List %s: %v", p, err)
	}
	return keys
}

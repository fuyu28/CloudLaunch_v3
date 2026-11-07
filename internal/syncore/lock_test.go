package syncore_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

func assertReleasedOnce(t *testing.T, e env) {
	t.Helper()
	acquired, held := e.lock.state()
	if acquired != 1 || held {
		t.Errorf("ロック: 取った回数 = %d、握ったまま = %v, want 1 回、解放済み", acquired, held)
	}
}

func TestLock(t *testing.T) {
	files := map[string]string{"slot1.sav": "one"}

	t.Run("3章/排他/ロックが取れなければ Push は ErrSyncInProgress で止まり、何も書かない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, files)
		e.lock.deny = true

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{}); !errors.Is(err, syncore.ErrSyncInProgress) {
			t.Fatalf("Push error = %v, want ErrSyncInProgress", err)
		}
		if keys := e.keys(t, "games/"); len(keys) != 0 {
			t.Errorf("リモートに書いた: %v", keys)
		}
		if base := e.base(t); base != "" {
			t.Errorf("base = %q, want 空", base)
		}
	})

	t.Run("3章/排他/ロックが取れなければ Pull は ErrSyncInProgress で止まり、何も書かない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, files)
		e.lock.deny = true

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); !errors.Is(err, syncore.ErrSyncInProgress) {
			t.Fatalf("Pull error = %v, want ErrSyncInProgress", err)
		}
		assertAborted(t, e, map[string]string{})
	})

	t.Run("3章/排他/ロックが取れなければ Recover は ErrSyncInProgress で止まり、何も変えない", func(t *testing.T) {
		e := newEnv(t)
		stage, _ := interruptedPull(t, e)
		writeFiles(t, stage, files)
		e.lock.deny = true

		got, err := e.core.Recover(t.Context())
		if !errors.Is(err, syncore.ErrSyncInProgress) {
			t.Fatalf("Recover error = %v, want ErrSyncInProgress", err)
		}
		if len(got) != 0 {
			t.Errorf("Recover = %v, want なし", got)
		}
		if diff := cmp.Diff(files, readFiles(t, stage)); diff != "" {
			t.Errorf("stage (-want +got):\n%s", diff)
		}
		if n := e.journalCount(t); n != 1 {
			t.Errorf("ジャーナルの行の数 = %d, want 1", n)
		}
	})

	t.Run("3章/排他/Push・Pull・Recover は終わった後にロックを解放する", func(t *testing.T) {
		tests := []struct {
			name string
			run  func(t *testing.T, e env) error
		}{
			{"Push の後", func(t *testing.T, e env) error {
				return e.core.Push(t.Context(), approvedPush(t, e, files), syncore.PushOptions{})
			}},
			{"Pull の後", func(t *testing.T, e env) error {
				return e.core.Pull(t.Context(), approvedPull(t, e, files), syncore.PullOptions{})
			}},
			{"Recover の後", func(t *testing.T, e env) error {
				interruptedPull(t, e)
				_, err := e.core.Recover(t.Context())
				return err
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				e := newEnv(t)
				if err := tt.run(t, e); err != nil {
					t.Fatalf("%v", err)
				}
				assertReleasedOnce(t, e)
			})
		}
	})

	t.Run("3章/排他/失敗して返るときもロックを解放する", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, files)
		injected := errors.New("injected")
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpPut {
				return injected
			}
			return nil
		}

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{}); !errors.Is(err, injected) {
			t.Fatalf("Push error = %v, want %v", err, injected)
		}
		assertReleasedOnce(t, e)
	})

	t.Run("3章/排他/Locker がなければ、何も変える前にエラー", func(t *testing.T) {
		withoutLocker := func(e env) *syncore.Core {
			return e.coreWithout(func(cfg *syncore.Config) { cfg.Locker = nil })
		}

		t.Run("Push", func(t *testing.T) {
			e := newEnv(t)
			plan := approvedPush(t, e, files)
			if err := withoutLocker(e).Push(t.Context(), plan, syncore.PushOptions{}); err == nil {
				t.Fatal("Push error = nil, want an error")
			}
			if keys := e.keys(t, "games/"); len(keys) != 0 {
				t.Errorf("リモートに書いた: %v", keys)
			}
		})

		t.Run("Pull", func(t *testing.T) {
			e := newEnv(t)
			plan := approvedPull(t, e, files)
			if err := withoutLocker(e).Pull(t.Context(), plan, syncore.PullOptions{}); err == nil {
				t.Fatal("Pull error = nil, want an error")
			}
			assertAborted(t, e, map[string]string{})
		})

		t.Run("Recover", func(t *testing.T) {
			e := newEnv(t)
			stage, _ := interruptedPull(t, e)
			writeFiles(t, stage, files)
			if _, err := withoutLocker(e).Recover(t.Context()); err == nil {
				t.Fatal("Recover error = nil, want an error")
			}
			if diff := cmp.Diff(files, readFiles(t, stage)); diff != "" {
				t.Errorf("stage (-want +got):\n%s", diff)
			}
			if n := e.journalCount(t); n != 1 {
				t.Errorf("ジャーナルの行の数 = %d, want 1", n)
			}
		})
	})
}

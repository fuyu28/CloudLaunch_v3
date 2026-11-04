package syncore_test

import (
	"errors"
	"io"
	"log/slog"
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

		if err := e.core.Push(t.Context(), plan); !errors.Is(err, syncore.ErrSyncInProgress) {
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

		if err := e.core.Pull(t.Context(), plan); !errors.Is(err, syncore.ErrSyncInProgress) {
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
			{"Push", func(t *testing.T, e env) error { return e.core.Push(t.Context(), approvedPush(t, e, files)) }},
			{"Pull", func(t *testing.T, e env) error { return e.core.Pull(t.Context(), approvedPull(t, e, files)) }},
			{"Recover", func(t *testing.T, e env) error {
				interruptedPull(t, e)
				_, err := e.core.Recover(t.Context())
				return err
			}},
		}
		for _, tt := range tests {
			e := newEnv(t)
			if err := tt.run(t, e); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			acquired, held := e.lock.state()
			if acquired != 1 || held {
				t.Errorf("%s のロック: 取った回数 = %d、握ったまま = %v, want 1 回、解放済み", tt.name, acquired, held)
			}
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

		if err := e.core.Push(t.Context(), plan); !errors.Is(err, injected) {
			t.Fatalf("Push error = %v, want %v", err, injected)
		}
		assertReleasedOnce(t, e)
	})

	t.Run("3章/排他/Locker がなければ、何も変える前にエラー", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, files)
		e.core = syncore.New(syncore.Config{
			Remote:     e.remote,
			Local:      e.store,
			Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
			DeviceName: testDeviceName,
			BackupsDir: e.backupsDir,
		})

		if err := e.core.Pull(t.Context(), plan); err == nil {
			t.Fatal("Pull error = nil, want an error")
		}
		assertAborted(t, e, map[string]string{})
	})
}

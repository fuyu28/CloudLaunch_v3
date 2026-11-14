package syncore_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

// fakeReupload は、ReuploadGame に渡す reupload の偽物。呼ばれた回数を数え、check があれば呼ばれた時点の状態を確かめさせる。
type fakeReupload struct {
	calls int
	err   error
	check func()
}

func (f *fakeReupload) run(context.Context) error {
	f.calls++
	if f.check != nil {
		f.check()
	}
	return f.err
}

// newDeletedEnv は、ゲームの tombstone の記録がある（上げ直せる）ゲームを用意する。
func newDeletedEnv(t *testing.T) env {
	t.Helper()
	e := newEnv(t)
	e.recordTombstone(t, testTombstone)
	return e
}

func TestReuploadGame(t *testing.T) {
	t.Run("8-U2 同期ロックを握ったまま上げ直し、終わったら外す", func(t *testing.T) {
		e := newDeletedEnv(t)
		re := &fakeReupload{}
		re.check = func() {
			if _, held := e.lock.state(); !held {
				t.Error("同期ロックを握らずに上げ直した")
			}
		}

		if err := e.core.ReuploadGame(t.Context(), testGameID, re.run); err != nil {
			t.Fatalf("ReuploadGame: %v", err)
		}
		if re.calls != 1 {
			t.Errorf("上げ直しを %d 回呼んだ, want 1 回", re.calls)
		}
		assertLockReleased(t, e)
	})

	t.Run("8-U2 同期ロックが取れなければ、何も変えずに拒否する", func(t *testing.T) {
		e := newDeletedEnv(t)
		e.lock.deny = true
		re := &fakeReupload{}

		if err := e.core.ReuploadGame(t.Context(), testGameID, re.run); !errors.Is(err, syncore.ErrSyncInProgress) {
			t.Fatalf("ReuploadGame error = %v, want ErrSyncInProgress", err)
		}
		if re.calls != 0 {
			t.Errorf("上げ直しを %d 回呼んだ", re.calls)
		}
	})

	t.Run("8-U2 ゲームの実行中でも上げ直す", func(t *testing.T) {
		e := newDeletedEnv(t)
		e.procs.runningFrom = 1
		re := &fakeReupload{}

		if err := e.core.ReuploadGame(t.Context(), testGameID, re.run); err != nil {
			t.Fatalf("ReuploadGame: %v", err)
		}
		if re.calls != 1 {
			t.Errorf("上げ直しを %d 回呼んだ, want 1 回", re.calls)
		}
		if asked := e.procs.asked(); len(asked) != 0 {
			t.Errorf("実行中かを確かめた: %v", asked)
		}
	})

	t.Run("8-U3 Pull ジャーナルが残っていれば、何も変える前に拒否する", func(t *testing.T) {
		e := newDeletedEnv(t)
		dir := filepath.Dir(e.saveDir)
		if err := e.store.InsertPullOperation(t.Context(), "0192d4e8-0000-7000-8000-0000000000cc", testGameID,
			e.saveDir, filepath.Join(dir, ".cloudlaunch-stage-x"), filepath.Join(dir, ".cloudlaunch-old-x"), testNow); err != nil {
			t.Fatalf("InsertPullOperation: %v", err)
		}
		re := &fakeReupload{}

		if err := e.core.ReuploadGame(t.Context(), testGameID, re.run); !errors.Is(err, syncore.ErrPullPending) {
			t.Fatalf("ReuploadGame error = %v, want ErrPullPending", err)
		}
		if re.calls != 0 {
			t.Errorf("上げ直しを %d 回呼んだ", re.calls)
		}
		assertLockReleased(t, e)
	})

	t.Run("8章/上げ直し/ゲームの tombstone の記録がなければ、何も変えずに拒否する", func(t *testing.T) {
		e := newEnv(t)
		re := &fakeReupload{}

		if err := e.core.ReuploadGame(t.Context(), testGameID, re.run); !errors.Is(err, syncore.ErrNotRemoteDeleted) {
			t.Fatalf("ReuploadGame error = %v, want ErrNotRemoteDeleted", err)
		}
		if re.calls != 0 {
			t.Errorf("上げ直しを %d 回呼んだ", re.calls)
		}
		assertLockReleased(t, e)
	})

	t.Run("8章/上げ直し/上げ直しのエラーを返し、同期ロックを外す", func(t *testing.T) {
		e := newDeletedEnv(t)
		injected := errors.New("injected: reupload")
		re := &fakeReupload{err: injected}

		if err := e.core.ReuploadGame(t.Context(), testGameID, re.run); !errors.Is(err, injected) {
			t.Fatalf("ReuploadGame error = %v, want %v", err, injected)
		}
		assertLockReleased(t, e)
	})

	t.Run("8章/上げ直し/reupload がなければ、ロックを取る前にエラーにする", func(t *testing.T) {
		e := newDeletedEnv(t)

		if err := e.core.ReuploadGame(t.Context(), testGameID, nil); err == nil {
			t.Fatal("ReuploadGame error = nil, want error")
		}
		if acquired, _ := e.lock.state(); acquired != 0 {
			t.Errorf("同期ロックを %d 回取った", acquired)
		}
	})
}

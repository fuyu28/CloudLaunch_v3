package syncore_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

func TestStatusTombstone(t *testing.T) {
	t.Run("8-T2 tombstone があれば判定しない", func(t *testing.T) {
		e := newEnv(t)
		files := map[string]string{"slot1.sav": "local"}
		writeFiles(t, e.saveDir, files)
		e.putRemoteFiles(t, map[string]string{"slot1.sav": "remote"})
		e.tombs.deletedFrom = 1
		// 判定しないので、HEAD を読めば失敗させる
		failOn(e, memstore.OpGet, "HEAD", errors.New("read HEAD"))

		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		want := syncore.Plan{GameID: testGameID, RemoteDeleted: &testTombstone}
		if diff := cmp.Diff(want, plan); diff != "" {
			t.Errorf("plan (-want +got):\n%s", diff)
		}
	})

	t.Run("8-T3 確認が失敗すれば、Status はエラーを返す", func(t *testing.T) {
		e := newEnv(t)
		injected := errors.New("read deleted.json")
		e.tombs.err = injected

		if _, err := e.status(t); !errors.Is(err, injected) {
			t.Errorf("err = %v, want %v", err, injected)
		}
	})

	t.Run("8章/tombstone/ReadTombstone がなければ、Status はエラーを返す", func(t *testing.T) {
		e := newEnv(t)
		core := e.coreWithout(func(cfg *syncore.Config) { cfg.ReadTombstone = nil })

		if _, err := core.Status(t.Context(), testGameID); err == nil {
			t.Error("エラーを返さなかった")
		}
	})
}

func TestPushPullTombstone(t *testing.T) {
	t.Run("8-T7 承認後に tombstone が書かれたら、Push は HEAD を書かずに止める", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, map[string]string{"slot1.sav": "local"})
		plan := e.planWant(t, syncore.DecisionPush)
		e.tombs.deletedFrom = 2

		err := e.core.Push(t.Context(), plan, syncore.PushOptions{})
		if !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Fatalf("err = %v, want ErrRemoteDeleted", err)
		}
		if _, ok := e.getObject(t, headKey()); ok {
			t.Error("HEAD を書いた")
		}
		if base := e.base(t); base != "" {
			t.Errorf("base = %q, want 空", base)
		}
	})

	t.Run("8-T7 承認後に tombstone が書かれたら、Pull は stage とジャーナルを削除して止める", func(t *testing.T) {
		e := newEnv(t)
		local := map[string]string{"slot1.sav": "local"}
		writeFiles(t, e.saveDir, local)
		e.setBase(t, syncore.ManifestHash(entriesOf(local)))
		e.putRemoteFiles(t, map[string]string{"slot1.sav": "remote"})
		plan := e.planWant(t, syncore.DecisionPull)
		e.tombs.deletedFrom = 2

		err := e.core.Pull(t.Context(), plan, syncore.PullOptions{})
		if !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Fatalf("err = %v, want ErrRemoteDeleted", err)
		}
		if diff := cmp.Diff(local, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダが変わった (-want +got):\n%s", diff)
		}
		if stages := e.workDirs(t, "stage"); len(stages) != 0 {
			t.Errorf("stage が残った: %v", stages)
		}
		if n := e.journalCount(t); n != 0 {
			t.Errorf("ジャーナルが %d 行残った", n)
		}
	})

	t.Run("8-T7 上書きで中身が同じときも、承認後に tombstone が書かれたら base を記録せずに止める", func(t *testing.T) {
		e := newEnv(t)
		files := map[string]string{"slot1.sav": "same"}
		writeFiles(t, e.saveDir, files)
		e.putRemoteFiles(t, files)
		plan := e.planWant(t, syncore.DecisionNoChange)
		e.tombs.deletedFrom = 2

		err := e.core.Push(t.Context(), plan, syncore.PushOptions{Overwrite: true})
		if !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Fatalf("err = %v, want ErrRemoteDeleted", err)
		}
		if base := e.base(t); base != "" {
			t.Errorf("base = %q, want 空", base)
		}
	})

	t.Run("8-T8 記録のあるゲームは、上書きでも Push・Pull を拒否する", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, map[string]string{"slot1.sav": "local"})
		e.putRemoteFiles(t, map[string]string{"slot1.sav": "remote"})
		e.tombs.deletedFrom = 1
		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{Overwrite: true}); !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Errorf("Push: err = %v, want ErrRemoteDeleted", err)
		}
		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{Overwrite: true}); !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Errorf("Pull: err = %v, want ErrRemoteDeleted", err)
		}
		if acquired, _ := e.lock.state(); acquired != 0 {
			t.Errorf("同期ロックを %d 回取った", acquired)
		}
		if _, ok := e.getObject(t, headKey()); !ok {
			t.Error("HEAD が消えた")
		}
		if diff := cmp.Diff(map[string]string{"slot1.sav": "local"}, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダが変わった (-want +got):\n%s", diff)
		}
	})
}

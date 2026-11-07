package syncore_test

import (
	"errors"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

func TestGameRunning(t *testing.T) {
	files := map[string]string{"slot1.sav": "one", "slot2.sav": "two"}

	t.Run("7章/実行中の確認/ゲームの実行中は Push を ErrGameRunning で拒否し、何も書かない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, files)
		e.procs.runningFrom = 1

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{}); !errors.Is(err, syncore.ErrGameRunning) {
			t.Fatalf("Push error = %v, want ErrGameRunning", err)
		}
		if diff := cmp.Diff([]string{e.exePath}, e.procs.asked()); diff != "" {
			t.Errorf("確かめた実行ファイル (-want +got):\n%s", diff)
		}
		if keys := e.keys(t, "games/"); len(keys) != 0 {
			t.Errorf("リモートに書いた: %v", keys)
		}
		if base := e.base(t); base != "" {
			t.Errorf("base = %q, want 空", base)
		}
	})

	t.Run("7章/実行中の確認/ゲームの実行中は Pull を ErrGameRunning で拒否し、何も書かない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, files)
		e.procs.runningFrom = 1

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); !errors.Is(err, syncore.ErrGameRunning) {
			t.Fatalf("Pull error = %v, want ErrGameRunning", err)
		}
		if diff := cmp.Diff([]string{e.exePath}, e.procs.asked()); diff != "" {
			t.Errorf("確かめた実行ファイル (-want +got):\n%s", diff)
		}
		assertAborted(t, e, map[string]string{})
	})

	t.Run("7章/実行中の確認/アップロードの途中でゲームが起動されたら、Push は manifest も HEAD も書かない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, files)
		e.procs.runningFrom = 2

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{}); !errors.Is(err, syncore.ErrGameRunning) {
			t.Fatalf("Push error = %v, want ErrGameRunning", err)
		}
		if keys := e.keys(t, "games/"+testGameID+"/manifests/"); len(keys) != 0 {
			t.Errorf("manifest を書いた: %v", keys)
		}
		if _, ok := e.getObject(t, headKey()); ok {
			t.Error("HEAD を書いた")
		}
		if base := e.base(t); base != "" {
			t.Errorf("base = %q, want 空", base)
		}
	})

	t.Run("7章/実行中の確認/ダウンロードの途中でゲームが起動されたら、Pull は入れ替えずに stage とジャーナルを消す", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, files)
		e.procs.runningFrom = 2

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); !errors.Is(err, syncore.ErrGameRunning) {
			t.Fatalf("Pull error = %v, want ErrGameRunning", err)
		}
		assertAborted(t, e, map[string]string{})
	})

	t.Run("7章/実行中の確認/実行ファイルが見つからなければ Push を ErrGameExeNotFound で拒否し、何も書かない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, files)
		if err := os.Remove(e.exePath); err != nil {
			t.Fatalf("remove exe: %v", err)
		}

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{}); !errors.Is(err, syncore.ErrGameExeNotFound) {
			t.Fatalf("Push error = %v, want ErrGameExeNotFound", err)
		}
		if keys := e.keys(t, "games/"); len(keys) != 0 {
			t.Errorf("リモートに書いた: %v", keys)
		}
	})

	t.Run("7章/実行中の確認/実行ファイルが見つからなければ Pull を ErrGameExeNotFound で拒否し、何も書かない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, files)
		if err := os.Remove(e.exePath); err != nil {
			t.Fatalf("remove exe: %v", err)
		}

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); !errors.Is(err, syncore.ErrGameExeNotFound) {
			t.Fatalf("Pull error = %v, want ErrGameExeNotFound", err)
		}
		assertAborted(t, e, map[string]string{})
	})

	t.Run("7章/実行中の確認/Processes がなければ、何も変える前にエラー", func(t *testing.T) {
		withoutProcesses := func(e env) *syncore.Core {
			return e.coreWithout(func(cfg *syncore.Config) { cfg.Processes = nil })
		}

		t.Run("Push", func(t *testing.T) {
			e := newEnv(t)
			plan := approvedPush(t, e, files)
			if err := withoutProcesses(e).Push(t.Context(), plan, syncore.PushOptions{}); err == nil {
				t.Fatal("Push error = nil, want an error")
			}
			if keys := e.keys(t, "games/"); len(keys) != 0 {
				t.Errorf("リモートに書いた: %v", keys)
			}
		})

		t.Run("Pull", func(t *testing.T) {
			e := newEnv(t)
			plan := approvedPull(t, e, files)
			if err := withoutProcesses(e).Pull(t.Context(), plan, syncore.PullOptions{}); err == nil {
				t.Fatal("Pull error = nil, want an error")
			}
			assertAborted(t, e, map[string]string{})
		})
	})
}

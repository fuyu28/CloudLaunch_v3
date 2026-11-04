package syncore_test

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

func isStageToLive(e env) func(op fileOp) bool {
	return func(op fileOp) bool {
		return strings.HasPrefix(filepath.Base(op.path), ".cloudlaunch-stage-") && op.to == e.saveDir
	}
}

func isPartialToBackup(op fileOp) bool {
	return strings.HasPrefix(filepath.Base(op.path), ".partial-")
}

func TestPullの書き出し(t *testing.T) {
	local := map[string]string{"slot1.sav": "old one", "dir/slot2.sav": "old two"}
	remote := map[string]string{"slot1.sav": "new one", "dir/slot2.sav": "new two", "dir/sub/slot3.sav": "new three"}
	injected := errors.New("injected")

	t.Run("6-L4 stage のファイルは、6-L7 のリネームの前にすべて書き出す", func(t *testing.T) {
		e := newEnv(t)
		plan := pullOverLocal(t, e, local, remote)

		if err := e.core.Pull(t.Context(), plan); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		want := []string{"dir/slot2.sav", "dir/sub/slot3.sav", "slot1.sav"}
		if diff := cmp.Diff(want, e.ops.syncedBeforeRename(t, isStageToLive(e))); diff != "" {
			t.Errorf("stage → live の前に書き出したファイル (-want +got):\n%s", diff)
		}
	})

	t.Run("6-A1 stage のファイルの書き出しに失敗したら中止し、stage とジャーナルを削除する", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, remote)
		e.ops.failSync = func(path string) error {
			if strings.HasSuffix(path, "slot3.sav") {
				return injected
			}
			return nil
		}

		if err := e.core.Pull(t.Context(), plan); !errors.Is(err, injected) {
			t.Fatalf("Pull error = %v, want %v", err, injected)
		}
		assertAborted(t, e, map[string]string{})
	})

	t.Run("6-L7 live を old に移した後に書き出しに失敗したら、ジャーナルを残してエラー", func(t *testing.T) {
		e := newEnv(t)
		plan := pullOverLocal(t, e, local, remote)
		// リネームは済み、その後の書き出しだけが失敗した状態にする
		e.rename.fn = func(oldpath, newpath string) error {
			if oldpath == e.saveDir {
				if err := os.Rename(oldpath, newpath); err != nil {
					return err
				}
				return injected
			}
			return nil
		}

		if err := e.core.Pull(t.Context(), plan); !errors.Is(err, injected) {
			t.Fatalf("Pull error = %v, want %v", err, injected)
		}
		if n := e.journalCount(t); n != 1 {
			t.Fatalf("ジャーナルの行の数 = %d, want 1", n)
		}

		// 残った状態は 6-R2 に当たり、復旧すると元のセーブに戻る
		e.rename.fn = nil
		got, err := e.core.Recover(t.Context())
		if err != nil {
			t.Fatalf("Recover: %v", err)
		}
		if len(got) != 1 || got[0].Case != syncore.RecoveryRestoreOld {
			t.Errorf("Recover = %+v, want 1 行の RecoveryRestoreOld", got)
		}
		if diff := cmp.Diff(local, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
		if dirs := append(e.workDirs(t, "stage"), e.workDirs(t, "old")...); len(dirs) != 0 {
			t.Errorf("作業用のディレクトリが残っている: %v", dirs)
		}
	})

	t.Run("6-L7 stage を live に移すリネームに失敗したら、ジャーナルを残してエラー", func(t *testing.T) {
		e := newEnv(t)
		plan := pullOverLocal(t, e, local, remote)
		e.rename.fn = func(oldpath, newpath string) error {
			if isStageToLive(e)(fileOp{kind: "rename", path: oldpath, to: newpath}) {
				return injected
			}
			return nil
		}

		if err := e.core.Pull(t.Context(), plan); !errors.Is(err, injected) {
			t.Fatalf("Pull error = %v, want %v", err, injected)
		}
		if n := e.journalCount(t); n != 1 {
			t.Errorf("ジャーナルの行の数 = %d, want 1", n)
		}
		if got := len(e.workDirs(t, "old")); got != 1 {
			t.Errorf("old の数 = %d, want 1", got)
		}
		if got := len(e.workDirs(t, "stage")); got != 1 {
			t.Errorf("stage の数 = %d, want 1", got)
		}
		if e.base(t) != syncore.ManifestHash(entriesOf(local)) {
			t.Errorf("base が変わった: %s", e.base(t))
		}
	})

	t.Run("6-L9 コピーしたファイルは、最終の名前へのリネームの前にすべて書き出す", func(t *testing.T) {
		e := newEnv(t)
		plan := pullOverLocal(t, e, local, remote)
		failRenameOldToBackups(e)

		if err := e.core.Pull(t.Context(), plan); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		want := []string{"dir/slot2.sav", "slot1.sav"}
		if diff := cmp.Diff(want, e.ops.syncedBeforeRename(t, isPartialToBackup)); diff != "" {
			t.Errorf(".partial → 最終の名前の前に書き出したファイル (-want +got):\n%s", diff)
		}
	})

	t.Run("6-L9 コピーしたファイルの書き出しに失敗したら、.partial を残さず、old とジャーナルを残してエラー", func(t *testing.T) {
		e := newEnv(t)
		plan := pullOverLocal(t, e, local, remote)
		failRenameOldToBackups(e)
		e.ops.failSync = func(path string) error {
			if strings.Contains(path, string(filepath.Separator)+".partial-") {
				return injected
			}
			return nil
		}

		if err := e.core.Pull(t.Context(), plan); !errors.Is(err, injected) {
			t.Fatalf("Pull error = %v, want %v", err, injected)
		}
		if names := e.gameBackups(t); len(names) != 0 {
			t.Errorf("バックアップのディレクトリに残っている: %v", names)
		}
		old := e.workDirs(t, "old")
		if len(old) != 1 {
			t.Fatalf("old = %v, want 1 つ", old)
		}
		if diff := cmp.Diff(local, readFiles(t, old[0])); diff != "" {
			t.Errorf("old (-want +got):\n%s", diff)
		}
		if n := e.journalCount(t); n != 1 {
			t.Errorf("ジャーナルの行の数 = %d, want 1", n)
		}
	})

	t.Run("6章/Pull/Rename がなければ、ジャーナルを書く前にエラー", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, remote)
		core := syncore.New(syncore.Config{
			Remote:     e.remote,
			Local:      e.store,
			Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
			DeviceName: testDeviceName,
			BackupsDir: e.backupsDir,
			Locker:     e.lock,
			Processes:  e.procs,
		})

		if err := core.Pull(t.Context(), plan); err == nil {
			t.Fatal("Pull error = nil, want an error")
		}
		assertAborted(t, e, map[string]string{})
	})
}

func TestRecoverの書き出し(t *testing.T) {
	local := map[string]string{"slot1.sav": "local one"}
	remote := map[string]string{"slot1.sav": "remote one"}
	injected := errors.New("injected")

	t.Run("6-R2 old を live に戻すリネームに失敗したら、行を残してエラー", func(t *testing.T) {
		e := newEnv(t)
		removeSaveDir(t, e)
		stage, old := interruptedPull(t, e)
		writeFiles(t, old, local)
		writeFiles(t, stage, remote)
		e.rename.fn = func(oldpath, _ string) error {
			if oldpath == old {
				return injected
			}
			return nil
		}

		if _, err := e.core.Recover(t.Context()); !errors.Is(err, injected) {
			t.Fatalf("Recover error = %v, want %v", err, injected)
		}
		if n := e.journalCount(t); n != 1 {
			t.Errorf("ジャーナルの行の数 = %d, want 1", n)
		}
		if diff := cmp.Diff(local, readFiles(t, old)); diff != "" {
			t.Errorf("old (-want +got):\n%s", diff)
		}
	})

	t.Run("6章/起動時の復旧/Rename がなければ、何も変える前にエラー", func(t *testing.T) {
		e := newEnv(t)
		stage, _ := interruptedPull(t, e)
		writeFiles(t, stage, remote)
		core := syncore.New(syncore.Config{
			Remote:     e.remote,
			Local:      e.store,
			Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
			DeviceName: testDeviceName,
			BackupsDir: e.backupsDir,
			Locker:     e.lock,
			Processes:  e.procs,
		})

		if _, err := core.Recover(t.Context()); err == nil {
			t.Fatal("Recover error = nil, want an error")
		}
		if diff := cmp.Diff(remote, readFiles(t, stage)); diff != "" {
			t.Errorf("stage (-want +got):\n%s", diff)
		}
		if n := e.journalCount(t); n != 1 {
			t.Errorf("ジャーナルの行の数 = %d, want 1", n)
		}
	})
}

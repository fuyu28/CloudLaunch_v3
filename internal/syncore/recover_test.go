package syncore_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/store"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

const (
	testOpID = "0192d4e8-0000-7000-8000-0000000000d1"
	// testBackupName は、testNow に始めた testOpID の Pull のバックアップの名前
	testBackupName = "20261103T010203.004Z-" + testOpID
)

// interruptedPull は、testOpID の Pull を中断した直後のジャーナルの行を書き、stage と old のパスを返す。
// ディレクトリは作らないので、呼び出し側が中断した時点の状態を作る。
func interruptedPull(t *testing.T, e env) (stage, old string) {
	t.Helper()
	parent := filepath.Dir(e.saveDir)
	stage = filepath.Join(parent, ".cloudlaunch-stage-"+testOpID)
	old = filepath.Join(parent, ".cloudlaunch-old-"+testOpID)
	if err := e.store.InsertPullOperation(context.Background(), testOpID, testGameID, e.saveDir, stage, old, testNow); err != nil {
		t.Fatalf("InsertPullOperation: %v", err)
	}
	return stage, old
}

func assertNotExist(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s が残っている (err = %v)", p, err)
		}
	}
}

func assertRecovered(t *testing.T, got []syncore.Recovery, want syncore.RecoveryCase) {
	t.Helper()
	if diff := cmp.Diff([]syncore.Recovery{{OpID: testOpID, GameID: testGameID, Case: want}}, got); diff != "" {
		t.Errorf("Recover (-want +got):\n%s", diff)
	}
}

func TestRecover(t *testing.T) {
	local := map[string]string{"slot1.sav": "local one", "dir/slot2.sav": "local two"}
	remote := map[string]string{"slot1.sav": "remote one"}

	t.Run("6-R1 stage があり、old がない → stage を削除する", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, local)
		stage, _ := interruptedPull(t, e)
		writeFiles(t, stage, remote)

		got, err := e.core.Recover(t.Context())
		if err != nil {
			t.Fatalf("Recover: %v", err)
		}
		assertRecovered(t, got, syncore.RecoveryRemoveStage)
		assertNotExist(t, stage)
		if diff := cmp.Diff(local, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
		if n := e.journalCount(t); n != 0 {
			t.Errorf("ジャーナルの行の数 = %d, want 0", n)
		}
	})

	t.Run("6-R2 old があり、live がない → old を live に戻し、stage を削除する", func(t *testing.T) {
		e := newEnv(t)
		removeSaveDir(t, e)
		stage, old := interruptedPull(t, e)
		writeFiles(t, old, local)
		writeFiles(t, stage, remote)

		got, err := e.core.Recover(t.Context())
		if err != nil {
			t.Fatalf("Recover: %v", err)
		}
		assertRecovered(t, got, syncore.RecoveryRestoreOld)
		assertNotExist(t, stage, old)
		if diff := cmp.Diff(local, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
		if n := e.journalCount(t); n != 0 {
			t.Errorf("ジャーナルの行の数 = %d, want 0", n)
		}
	})

	t.Run("6-R3 old と live があり、stage がない → old をバックアップへ移し、base は書かない", func(t *testing.T) {
		e := newEnv(t)
		localHash := syncore.ManifestHash(entriesOf(local))
		e.setBase(t, localHash)
		writeFiles(t, e.saveDir, remote)
		_, old := interruptedPull(t, e)
		writeFiles(t, old, local)

		got, err := e.core.Recover(t.Context())
		if err != nil {
			t.Fatalf("Recover: %v", err)
		}
		assertRecovered(t, got, syncore.RecoveryBackupOld)
		assertNotExist(t, old)
		if diff := cmp.Diff(remote, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(local, readFiles(t, filepath.Join(e.backupsDir, testGameID, testBackupName))); diff != "" {
			t.Errorf("バックアップ (-want +got):\n%s", diff)
		}
		if got := e.base(t); got != localHash {
			t.Errorf("base = %q, want 変わらない %q", got, localHash)
		}
		if n := e.journalCount(t); n != 0 {
			t.Errorf("ジャーナルの行の数 = %d, want 0", n)
		}
	})

	t.Run("6-R4 stage も old もない → 何もせず行を消す", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, local)
		interruptedPull(t, e)

		got, err := e.core.Recover(t.Context())
		if err != nil {
			t.Fatalf("Recover: %v", err)
		}
		assertRecovered(t, got, syncore.RecoveryNothing)
		if diff := cmp.Diff(local, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
		if n := e.journalCount(t); n != 0 {
			t.Errorf("ジャーナルの行の数 = %d, want 0", n)
		}
	})

	t.Run("6-R5 old・live・stage がすべてある → 行を残してエラー", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, map[string]string{"new.sav": "made by game"})
		stage, old := interruptedPull(t, e)
		writeFiles(t, old, local)
		writeFiles(t, stage, remote)

		got, err := e.core.Recover(t.Context())
		if err == nil {
			t.Fatal("Recover error = nil, want an error")
		}
		if len(got) != 0 {
			t.Errorf("Recover = %v, want なし", got)
		}
		if diff := cmp.Diff(local, readFiles(t, old)); diff != "" {
			t.Errorf("old (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(remote, readFiles(t, stage)); diff != "" {
			t.Errorf("stage (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(map[string]string{"new.sav": "made by game"}, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
		if n := e.journalCount(t); n != 1 {
			t.Errorf("ジャーナルの行の数 = %d, want 1", n)
		}
	})

	t.Run("6章/起動時の復旧/パスの状態を確かめられない → 行を残してエラー", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("ディレクトリの権限で Lstat を失敗させられない")
		}
		e := newEnv(t)
		writeFiles(t, e.saveDir, local)
		parent := filepath.Dir(e.saveDir)
		// stage と old を、読めないディレクトリの下にあることにする
		locked := filepath.Join(parent, "locked")
		if err := os.Mkdir(locked, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		stage := filepath.Join(locked, ".cloudlaunch-stage-"+testOpID)
		writeFiles(t, stage, remote)
		if err := e.store.InsertPullOperation(context.Background(), testOpID, testGameID, e.saveDir, stage,
			filepath.Join(locked, ".cloudlaunch-old-"+testOpID), testNow); err != nil {
			t.Fatalf("InsertPullOperation: %v", err)
		}
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

		got, err := e.core.Recover(t.Context())
		if err == nil {
			t.Fatal("Recover error = nil, want an error")
		}
		if len(got) != 0 {
			t.Errorf("Recover = %v, want なし", got)
		}
		if n := e.journalCount(t); n != 1 {
			t.Errorf("ジャーナルの行の数 = %d, want 1", n)
		}
		if diff := cmp.Diff(local, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
	})

	t.Run("6章/起動時の復旧/最終の名前のバックアップがある → コピーし直さずに old を削除する", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, remote)
		_, old := interruptedPull(t, e)
		// コピーを終えて最終の名前にした後、old を消している途中で中断した
		writeFiles(t, filepath.Join(e.backupsDir, testGameID, testBackupName), local)
		writeFiles(t, old, map[string]string{"slot1.sav": "local one"})

		if _, err := e.core.Recover(t.Context()); err != nil {
			t.Fatalf("Recover: %v", err)
		}
		assertNotExist(t, old)
		if diff := cmp.Diff(local, readFiles(t, filepath.Join(e.backupsDir, testGameID, testBackupName))); diff != "" {
			t.Errorf("バックアップ (-want +got):\n%s", diff)
		}
	})

	t.Run("6章/起動時の復旧/.partial が残っている → 削除してからコピーし直す", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, remote)
		_, old := interruptedPull(t, e)
		writeFiles(t, old, local)
		partial := filepath.Join(e.backupsDir, testGameID, ".partial-"+testBackupName)
		writeFiles(t, partial, map[string]string{"slot1.sav": "loc", "stale.sav": "stale"})
		failRenameOldToBackups(e)

		if _, err := e.core.Recover(t.Context()); err != nil {
			t.Fatalf("Recover: %v", err)
		}
		assertNotExist(t, old, partial)
		if diff := cmp.Diff(local, readFiles(t, filepath.Join(e.backupsDir, testGameID, testBackupName))); diff != "" {
			t.Errorf("バックアップ (-want +got):\n%s", diff)
		}
	})

	t.Run("6章/起動時の復旧/ある行で失敗しても残りの行を処理する", func(t *testing.T) {
		e := newEnv(t)
		// 1 つ目のゲームは 6-R5 に当たって失敗する
		writeFiles(t, e.saveDir, local)
		stage, old := interruptedPull(t, e)
		writeFiles(t, old, local)
		writeFiles(t, stage, remote)

		// 2 つ目のゲームは 6-R1 に当たって復旧できる
		const otherGameID = "0192d4e8-0000-7000-8000-000000000002"
		const otherOpID = "0192d4e8-0000-7000-8000-0000000000d2"
		otherSave := filepath.Join(t.TempDir(), "other")
		otherStage := filepath.Join(filepath.Dir(otherSave), ".cloudlaunch-stage-"+otherOpID)
		ctx := context.Background()
		if err := e.store.CreateGame(ctx, store.NewGame{ID: otherGameID, Title: "Other", SaveFolderPath: otherSave, ExePath: otherSave + ".exe", CreatedAt: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)}); err != nil {
			t.Fatalf("CreateGame: %v", err)
		}
		if err := e.store.InsertPullOperation(ctx, otherOpID, otherGameID, otherSave,
			otherStage, filepath.Join(filepath.Dir(otherSave), ".cloudlaunch-old-"+otherOpID), testNow); err != nil {
			t.Fatalf("InsertPullOperation: %v", err)
		}
		writeFiles(t, otherStage, remote)

		got, err := e.core.Recover(t.Context())
		if err == nil {
			t.Fatal("Recover error = nil, want an error")
		}
		want := []syncore.Recovery{{OpID: otherOpID, GameID: otherGameID, Case: syncore.RecoveryRemoveStage}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Recover (-want +got):\n%s", diff)
		}
		assertNotExist(t, otherStage)
		if n := e.journalCount(t); n != 1 {
			t.Errorf("ジャーナルの行の数 = %d, want 1（失敗した行だけ）", n)
		}
	})
}

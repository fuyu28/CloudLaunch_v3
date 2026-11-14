package syncore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/store"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

// fakeRemoteDeletion は、DeleteGame に渡す deleteRemote の偽物。呼ばれた回数を数え、check があれば呼ばれた時点の状態を
// 確かめさせる。
type fakeRemoteDeletion struct {
	calls int
	err   error
	check func()
}

func (f *fakeRemoteDeletion) run(context.Context) error {
	f.calls++
	if f.check != nil {
		f.check()
	}
	return f.err
}

func (e env) deleteGame(t *testing.T, del *fakeRemoteDeletion) error {
	t.Helper()
	_, err := e.core.DeleteGame(t.Context(), testGameID, del.run)
	return err
}

var errUnregister = errors.New("injected: unregister")

// failingUnregister は、登録の解除だけを失敗させる LocalStore。
type failingUnregister struct {
	*store.Store
}

func (failingUnregister) DeleteGame(context.Context, string) error { return errUnregister }

func (e env) registered(t *testing.T) bool {
	t.Helper()
	var n int
	if err := e.openDB(t).QueryRow(`SELECT count(*) FROM games WHERE id = ?`, testGameID).Scan(&n); err != nil {
		t.Fatalf("count games: %v", err)
	}
	return n == 1
}

// assertLockReleased は、DeleteGame が同期ロックを取ったなら、戻る前に外したことを確かめる。
func assertLockReleased(t *testing.T, e env) {
	t.Helper()
	if _, held := e.lock.state(); held {
		t.Error("同期ロックを外していない")
	}
}

// assertNotDeleted は、DeleteGame が何も変えずに止まったこと（リモートの削除を呼ばず、バックアップを作らず、登録が残る）を
// 確かめる。
func assertNotDeleted(t *testing.T, e env, del *fakeRemoteDeletion) {
	t.Helper()
	if del.calls != 0 {
		t.Errorf("リモートの削除を %d 回呼んだ", del.calls)
	}
	if names := e.gameBackups(t); len(names) != 0 {
		t.Errorf("バックアップを作った: %v", names)
	}
	if !e.registered(t) {
		t.Error("登録を解除した")
	}
	assertLockReleased(t, e)
}

func TestDeleteGame(t *testing.T) {
	files := map[string]string{"slot1.sav": "one", "dir/slot2.sav": "two"}

	t.Run("8-X2 ゲームの実行中なら、何も変えずに拒否する", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		e.procs.runningFrom = 1
		del := &fakeRemoteDeletion{}

		if err := e.deleteGame(t, del); !errors.Is(err, syncore.ErrGameRunning) {
			t.Fatalf("DeleteGame error = %v, want ErrGameRunning", err)
		}
		if diff := cmp.Diff([]string{e.exePath}, e.procs.asked()); diff != "" {
			t.Errorf("確かめた実行ファイル (-want +got):\n%s", diff)
		}
		assertNotDeleted(t, e, del)
	})

	t.Run("8-X2 バックアップの間にゲームが起動されたら、リモートを削除せずに止める", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		e.procs.runningFrom = 2
		del := &fakeRemoteDeletion{}

		if err := e.deleteGame(t, del); !errors.Is(err, syncore.ErrGameRunning) {
			t.Fatalf("DeleteGame error = %v, want ErrGameRunning", err)
		}
		// バックアップは残り、8-X8 と同じ状態になる
		if del.calls != 0 || !e.registered(t) || len(e.gameBackups(t)) != 1 {
			t.Errorf("リモートの削除 %d 回・登録 %v・バックアップ %v, want 0 回・残る・1 つ", del.calls, e.registered(t), e.gameBackups(t))
		}
		assertLockReleased(t, e)
	})

	t.Run("8-X2 同期ロックが取れなければ、何も変えずに拒否する", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		e.lock.deny = true
		del := &fakeRemoteDeletion{}

		if err := e.deleteGame(t, del); !errors.Is(err, syncore.ErrSyncInProgress) {
			t.Fatalf("DeleteGame error = %v, want ErrSyncInProgress", err)
		}
		assertNotDeleted(t, e, del)
	})

	t.Run("8-X3 Pull ジャーナルが残っていれば、何も変えずに拒否する", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		dir := filepath.Dir(e.saveDir)
		if err := e.store.InsertPullOperation(t.Context(), "0192d4e8-0000-7000-8000-0000000000cc", testGameID,
			e.saveDir, filepath.Join(dir, ".cloudlaunch-stage-x"), filepath.Join(dir, ".cloudlaunch-old-x"), testNow); err != nil {
			t.Fatalf("InsertPullOperation: %v", err)
		}
		del := &fakeRemoteDeletion{}

		if err := e.deleteGame(t, del); !errors.Is(err, syncore.ErrPullPending) {
			t.Fatalf("DeleteGame error = %v, want ErrPullPending", err)
		}
		assertNotDeleted(t, e, del)
	})

	t.Run("8-X4 セーブをバックアップへコピーし、セーブフォルダを残す", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		del := &fakeRemoteDeletion{}

		res, err := e.core.DeleteGame(t.Context(), testGameID, del.run)
		if err != nil {
			t.Fatalf("DeleteGame: %v", err)
		}
		names := e.gameBackups(t)
		if len(names) != 1 || !backupNamePattern.MatchString(names[0]) {
			t.Fatalf("バックアップ = %v, want testNow の名前で 1 つ", names)
		}
		backup := filepath.Join(e.backupsDir, testGameID, names[0])
		if res.BackupPath != backup {
			t.Errorf("BackupPath = %q, want %q", res.BackupPath, backup)
		}
		if diff := cmp.Diff(files, readFiles(t, backup)); diff != "" {
			t.Errorf("バックアップ (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(files, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
	})

	t.Run("8-X4 バックアップを 3 個に保ち、その削除で作ったものは消さない", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		// 時計が戻ったときのように、testNow より新しい名前を 3 つ置く。作ったものは名前の順で古い側に並ぶ
		older := "20261001T000000.000Z-0192d4e8-0000-7000-8000-000000000101"
		newer := []string{
			"20261201T000000.000Z-0192d4e8-0000-7000-8000-000000000102",
			"20261202T000000.000Z-0192d4e8-0000-7000-8000-000000000103",
			"20261203T000000.000Z-0192d4e8-0000-7000-8000-000000000104",
		}
		mkdirBackups(t, e, append([]string{older}, newer...)...)
		del := &fakeRemoteDeletion{}

		if err := e.deleteGame(t, del); err != nil {
			t.Fatalf("DeleteGame: %v", err)
		}
		names := e.gameBackups(t)
		if len(names) != 4 || !backupNamePattern.MatchString(names[0]) {
			t.Fatalf("バックアップ = %v, want 作ったものと新しい 3 つ", names)
		}
		if diff := cmp.Diff(newer, names[1:]); diff != "" {
			t.Errorf("残ったバックアップ (-want +got):\n%s", diff)
		}
	})

	t.Run("8-X7 バックアップ → リモートの削除 → 登録の解除、の順で行う", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		del := &fakeRemoteDeletion{}
		del.check = func() {
			if names := e.gameBackups(t); len(names) != 1 {
				t.Errorf("リモートの削除の時点のバックアップ = %v, want 1 つ", names)
			}
			if !e.registered(t) {
				t.Error("リモートの削除より先に登録を解除した")
			}
			if _, held := e.lock.state(); !held {
				t.Error("リモートの削除の間、同期ロックを握っていない")
			}
		}

		if err := e.deleteGame(t, del); err != nil {
			t.Fatalf("DeleteGame: %v", err)
		}
		if del.calls != 1 {
			t.Errorf("リモートの削除を %d 回呼んだ, want 1", del.calls)
		}
		if e.registered(t) {
			t.Error("登録を解除していない")
		}
		// 登録を解除しても、バックアップは消さない（8-X4）
		if names := e.gameBackups(t); len(names) != 1 {
			t.Errorf("バックアップ = %v, want 1 つ", names)
		}
		assertLockReleased(t, e)
	})

	t.Run("8-X7 登録の解除に失敗したら、ErrUnregisterFailed を返す", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		core := e.coreWithout(func(cfg *syncore.Config) { cfg.Local = failingUnregister{e.store} })
		del := &fakeRemoteDeletion{}

		_, err := core.DeleteGame(t.Context(), testGameID, del.run)
		if !errors.Is(err, syncore.ErrUnregisterFailed) || !errors.Is(err, errUnregister) {
			t.Fatalf("DeleteGame error = %v, want ErrUnregisterFailed で包んだ元のエラー", err)
		}
		if errors.Is(err, syncore.ErrRemoteDeletionFailed) {
			t.Error("リモートの削除の失敗として返した")
		}
		if del.calls != 1 {
			t.Errorf("リモートの削除を %d 回呼んだ, want 1", del.calls)
		}
		assertLockReleased(t, e)
	})

	t.Run("8-X8 バックアップの後で止まった状態から、もう一度削除できる", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		// 中断した削除が作ったバックアップだけが残り、リモートも登録も変わっていない
		const interrupted = "20261102T000000.000Z-0192d4e8-0000-7000-8000-000000000105"
		writeFiles(t, filepath.Join(e.backupsDir, testGameID, interrupted), files)
		del := &fakeRemoteDeletion{}

		if err := e.deleteGame(t, del); err != nil {
			t.Fatalf("DeleteGame: %v", err)
		}
		names := e.gameBackups(t)
		if len(names) != 2 || names[0] != interrupted || !backupNamePattern.MatchString(names[1]) {
			t.Errorf("バックアップ = %v, want 中断したときのものと、作ったもの", names)
		}
		if del.calls != 1 || e.registered(t) {
			t.Errorf("リモートの削除 %d 回・登録 %v, want 1 回・解除", del.calls, e.registered(t))
		}
	})

	t.Run("8-X9 リモートの削除に失敗したら、登録を残す", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		injected := errors.New("injected: put deleted.json")
		del := &fakeRemoteDeletion{err: injected}

		err := e.deleteGame(t, del)
		if !errors.Is(err, syncore.ErrRemoteDeletionFailed) || !errors.Is(err, injected) {
			t.Fatalf("DeleteGame error = %v, want ErrRemoteDeletionFailed で包んだ元のエラー", err)
		}
		if !e.registered(t) {
			t.Error("登録を解除した")
		}
		if names := e.gameBackups(t); len(names) != 1 {
			t.Errorf("バックアップ = %v, want 1 つ", names)
		}
		assertLockReleased(t, e)
	})

	t.Run("8-X11 ゲームの tombstone があれば、何も変えずに拒否する", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		e.tombs.deletedFrom = 1
		del := &fakeRemoteDeletion{}

		if err := e.deleteGame(t, del); !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Fatalf("DeleteGame error = %v, want ErrRemoteDeleted", err)
		}
		assertNotDeleted(t, e, del)
	})

	t.Run("8-X11 バックアップの間にゲームの tombstone が書かれたら、リモートを削除せずに止める", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		e.tombs.deletedFrom = 2
		del := &fakeRemoteDeletion{}

		if err := e.deleteGame(t, del); !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Fatalf("DeleteGame error = %v, want ErrRemoteDeleted", err)
		}
		if del.calls != 0 || !e.registered(t) {
			t.Errorf("リモートの削除 %d 回・登録 %v, want 0 回・残る", del.calls, e.registered(t))
		}
		assertLockReleased(t, e)
	})

	t.Run("8-X11 ゲームの tombstone を確かめられなければ、何も変えずに止める", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		injected := errors.New("injected: invalid deleted.json")
		e.tombs.err = injected
		del := &fakeRemoteDeletion{}

		err := e.deleteGame(t, del)
		if !errors.Is(err, injected) || errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Fatalf("DeleteGame error = %v, want ErrRemoteDeleted でない元のエラー", err)
		}
		assertNotDeleted(t, e, del)
	})

	t.Run("8-X12 セーブフォルダがなければ、バックアップを作らずに削除する", func(t *testing.T) {
		e := newEnv(t)
		if err := os.Remove(e.saveDir); err != nil {
			t.Fatalf("remove save folder: %v", err)
		}
		del := &fakeRemoteDeletion{}

		res, err := e.core.DeleteGame(t.Context(), testGameID, del.run)
		if err != nil {
			t.Fatalf("DeleteGame: %v", err)
		}
		if names := e.gameBackups(t); len(names) != 0 {
			t.Errorf("バックアップを作った: %v", names)
		}
		if res.BackupPath != "" {
			t.Errorf("BackupPath = %q, want 空", res.BackupPath)
		}
		if del.calls != 1 || e.registered(t) {
			t.Errorf("リモートの削除 %d 回・登録 %v, want 1 回・解除", del.calls, e.registered(t))
		}
	})

	t.Run("8-X13 実行ファイルがなければ、実行中でないとして削除する", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		if err := os.Remove(e.exePath); err != nil {
			t.Fatalf("remove exe: %v", err)
		}
		del := &fakeRemoteDeletion{}

		if err := e.deleteGame(t, del); err != nil {
			t.Fatalf("DeleteGame: %v", err)
		}
		if del.calls != 1 || e.registered(t) {
			t.Errorf("リモートの削除 %d 回・登録 %v, want 1 回・解除", del.calls, e.registered(t))
		}
	})

	t.Run("8章/ゲームの削除/バックアップのコピーに失敗したら、リモートに触れず、途中までのコピーを残さない", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		// 通常のファイルでないものはコピーできない（copyTree）
		if err := os.Symlink("slot1.sav", filepath.Join(e.saveDir, "link.sav")); err != nil {
			t.Skipf("symlink を作れない: %v", err)
		}
		del := &fakeRemoteDeletion{}

		if err := e.deleteGame(t, del); err == nil {
			t.Fatal("エラーを返さなかった")
		}
		assertNotDeleted(t, e, del)
	})

	t.Run("8章/ゲームの削除/バックアップのリネームに失敗したら、リモートに触れず、途中までのコピーを残さない", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		e.rename.fn = func(oldpath, _ string) error {
			if strings.HasPrefix(filepath.Base(oldpath), ".partial-") {
				return errors.New("injected: rename partial backup")
			}
			return nil
		}
		del := &fakeRemoteDeletion{}

		if err := e.deleteGame(t, del); err == nil {
			t.Fatal("エラーを返さなかった")
		}
		assertNotDeleted(t, e, del)
	})

	t.Run("8章/ゲームの削除/必須の設定がなければ、ロックを取る前にエラー", func(t *testing.T) {
		cases := []struct {
			name string
			edit func(cfg *syncore.Config)
		}{
			{"Locker", func(cfg *syncore.Config) { cfg.Locker = nil }},
			{"Processes", func(cfg *syncore.Config) { cfg.Processes = nil }},
			{"ReadTombstone", func(cfg *syncore.Config) { cfg.ReadTombstone = nil }},
			{"BackupsDir", func(cfg *syncore.Config) { cfg.BackupsDir = "" }},
			{"Rename", func(cfg *syncore.Config) { cfg.Rename = nil }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				e := newEnv(t)
				writeFiles(t, e.saveDir, files)
				del := &fakeRemoteDeletion{}

				if _, err := e.coreWithout(tc.edit).DeleteGame(t.Context(), testGameID, del.run); err == nil {
					t.Fatal("エラーを返さなかった")
				}
				if acquired, _ := e.lock.state(); acquired != 0 {
					t.Errorf("同期ロックを %d 回取った", acquired)
				}
				assertNotDeleted(t, e, del)
			})
		}

		t.Run("deleteRemote", func(t *testing.T) {
			e := newEnv(t)
			if _, err := e.core.DeleteGame(t.Context(), testGameID, nil); err == nil {
				t.Fatal("エラーを返さなかった")
			}
			if acquired, _ := e.lock.state(); acquired != 0 {
				t.Errorf("同期ロックを %d 回取った", acquired)
			}
			if !e.registered(t) {
				t.Error("登録を解除した")
			}
		})
	})
}

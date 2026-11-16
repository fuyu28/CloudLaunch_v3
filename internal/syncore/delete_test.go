package syncore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

// deletedByTest は、テストのコアがゲームの削除で書く deleted.json と、DB に書く記録（Config.Now と DeviceName から作る）。
const deletedByTest = `{"deletedAt":"2026-11-03T01:02:03.004Z","deviceName":"Desktop"}`

var deletedByTestTombstone = syncore.Tombstone{DeletedAt: testNow, DeviceName: testDeviceName}

// remoteDeletion は、DeleteGame が deleted.json を PUT しようとした回数を数える。check があれば、PUT の前に呼び、その時点の
// 状態を確かめさせる。
type remoteDeletion struct {
	calls int
	check func()
}

// watchDeletion は、それまでの Before を続けて呼ぶので、failOn などで Before を差し替えた後に呼ぶ。
func (e env) watchDeletion() *remoteDeletion {
	d := &remoteDeletion{}
	prev := e.remote.Before
	e.remote.Before = func(op memstore.Op, key string) error {
		if op == memstore.OpPut && key == tombstoneKey() {
			d.calls++
			if d.check != nil {
				d.check()
			}
		}
		if prev != nil {
			return prev(op, key)
		}
		return nil
	}
	return d
}

func (e env) deleteGame(t *testing.T) error {
	t.Helper()
	_, err := e.core.DeleteGame(t.Context(), testGameID)
	return err
}

var errUnregister = errors.New("injected: unregister")

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

func assertLockReleased(t *testing.T, e env) {
	t.Helper()
	if _, held := e.lock.state(); held {
		t.Error("同期ロックを外していない")
	}
}

// assertNotDeleted は、DeleteGame が何も変えずに止まったこと（deleted.json を PUT せず、バックアップを作らず、登録が残る）を
// 確かめる。
func assertNotDeleted(t *testing.T, e env, del *remoteDeletion) {
	t.Helper()
	if del.calls != 0 {
		t.Errorf("deleted.json を %d 回 PUT した", del.calls)
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
		del := e.watchDeletion()

		if err := e.deleteGame(t); !errors.Is(err, syncore.ErrGameRunning) {
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
		del := e.watchDeletion()

		if err := e.deleteGame(t); !errors.Is(err, syncore.ErrGameRunning) {
			t.Fatalf("DeleteGame error = %v, want ErrGameRunning", err)
		}
		// バックアップは残り、8-X8 と同じ状態になる
		if del.calls != 0 || !e.registered(t) || len(e.gameBackups(t)) != 1 {
			t.Errorf("deleted.json の PUT %d 回・登録 %v・バックアップ %v, want 0 回・残る・1 つ", del.calls, e.registered(t), e.gameBackups(t))
		}
		assertLockReleased(t, e)
	})

	t.Run("8-X2 同期ロックが取れなければ、何も変えずに拒否する", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		e.lock.deny = true
		del := e.watchDeletion()

		if err := e.deleteGame(t); !errors.Is(err, syncore.ErrSyncInProgress) {
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
		del := e.watchDeletion()

		if err := e.deleteGame(t); !errors.Is(err, syncore.ErrPullPending) {
			t.Fatalf("DeleteGame error = %v, want ErrPullPending", err)
		}
		assertNotDeleted(t, e, del)
	})

	t.Run("8-X4 セーブをバックアップへコピーし、セーブフォルダを残す", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)

		res, err := e.core.DeleteGame(t.Context(), testGameID)
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

		if err := e.deleteGame(t); err != nil {
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

	t.Run("8-X5 deleted.json を PUT し、DB にゲームの tombstone を記録する", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		e.putGameData(t)
		del := e.watchDeletion()
		var recordedAtPut bool
		del.check = func() { _, recordedAtPut = e.recorded(t) }
		// 登録を解除すると記録も消えるので、解除に失敗させて記録を読めるようにする
		core := e.coreWithout(func(cfg *syncore.Config) { cfg.Local = failingUnregister{e.store} })

		if _, err := core.DeleteGame(t.Context(), testGameID); !errors.Is(err, syncore.ErrUnregisterFailed) {
			t.Fatalf("DeleteGame error = %v, want ErrUnregisterFailed", err)
		}
		if body, _ := e.getObject(t, tombstoneKey()); body != deletedByTest {
			t.Errorf("deleted.json = %s, want %s", body, deletedByTest)
		}
		if recordedAtPut {
			t.Error("deleted.json の PUT より先に DB に記録した")
		}
		rec, recOK := e.recorded(t)
		if !recOK {
			t.Fatal("DB に記録しなかった")
		}
		if diff := cmp.Diff(deletedByTestTombstone, rec); diff != "" {
			t.Errorf("記録 (-want +got):\n%s", diff)
		}
	})

	t.Run("8-X6 配下のゲームの tombstone 以外を削除し、ほかのゲームのキーは残す", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		e.putGameData(t)

		if err := e.deleteGame(t); err != nil {
			t.Fatalf("DeleteGame: %v", err)
		}
		if diff := cmp.Diff(afterCleanup(), e.keys(t, "games/")); diff != "" {
			t.Errorf("残ったキー (-want +got):\n%s", diff)
		}
	})

	t.Run("8-X7 バックアップ → リモートの削除 → 登録の解除、の順で行う", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		del := e.watchDeletion()
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

		if err := e.deleteGame(t); err != nil {
			t.Fatalf("DeleteGame: %v", err)
		}
		if del.calls != 1 {
			t.Errorf("deleted.json を %d 回 PUT した, want 1", del.calls)
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
		del := e.watchDeletion()

		_, err := core.DeleteGame(t.Context(), testGameID)
		if !errors.Is(err, syncore.ErrUnregisterFailed) || !errors.Is(err, errUnregister) {
			t.Fatalf("DeleteGame error = %v, want ErrUnregisterFailed で包んだ元のエラー", err)
		}
		if errors.Is(err, syncore.ErrRemoteDeletionFailed) {
			t.Error("リモートの削除の失敗として返した")
		}
		if del.calls != 1 {
			t.Errorf("deleted.json を %d 回 PUT した, want 1", del.calls)
		}
		assertLockReleased(t, e)
	})

	t.Run("8-X8 バックアップの後で止まった状態から、もう一度削除できる", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		// 中断した削除が作ったバックアップだけが残り、リモートも登録も変わっていない
		const interrupted = "20261102T000000.000Z-0192d4e8-0000-7000-8000-000000000105"
		writeFiles(t, filepath.Join(e.backupsDir, testGameID, interrupted), files)
		del := e.watchDeletion()

		if err := e.deleteGame(t); err != nil {
			t.Fatalf("DeleteGame: %v", err)
		}
		names := e.gameBackups(t)
		if len(names) != 2 || names[0] != interrupted || !backupNamePattern.MatchString(names[1]) {
			t.Errorf("バックアップ = %v, want 中断したときのものと、作ったもの", names)
		}
		if del.calls != 1 || e.registered(t) {
			t.Errorf("deleted.json の PUT %d 回・登録 %v, want 1 回・解除", del.calls, e.registered(t))
		}
	})

	t.Run("8-X9 リモートの削除に失敗したら、登録を残す", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		injected := errors.New("injected: put deleted.json")
		failOn(e, memstore.OpPut, "deleted.json", injected)

		err := e.deleteGame(t)
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

	t.Run("8-X9 deleted.json の PUT に失敗したら、記録も削除もしない", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		e.putGameData(t)
		before := e.keys(t, "games/")
		injected := errors.New("injected: put deleted.json")
		failOn(e, memstore.OpPut, "deleted.json", injected)

		err := e.deleteGame(t)
		if !errors.Is(err, injected) || errors.Is(err, syncore.ErrDeleteIncomplete) {
			t.Fatalf("DeleteGame error = %v, want ErrDeleteIncomplete でない元のエラー", err)
		}
		if _, rec := e.recorded(t); rec {
			t.Error("DB に記録した")
		}
		if diff := cmp.Diff(before, e.keys(t, "games/")); diff != "" {
			t.Errorf("リモートのキーが変わった (-before +after):\n%s", diff)
		}
	})

	t.Run("8-X10 PUT の後で配下の削除に失敗したら、記録を残し、次の tombstone の確認で消し残りを削除する", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		e.putGameData(t)
		injected := errors.New("injected: delete game.json")
		failOn(e, memstore.OpDelete, "/game.json", injected)

		err := e.deleteGame(t)
		if !errors.Is(err, syncore.ErrDeleteIncomplete) || !errors.Is(err, injected) {
			t.Fatalf("DeleteGame error = %v, want ErrDeleteIncomplete で包んだ元のエラー", err)
		}
		if _, rec := e.recorded(t); !rec {
			t.Error("DB に記録しなかった")
		}
		if !e.registered(t) {
			t.Error("登録を解除した")
		}
		if body, _ := e.getObject(t, tombstoneKey()); body != deletedByTest {
			t.Errorf("deleted.json = %s, want %s", body, deletedByTest)
		}

		e.remote.Before = nil
		got, ok, err := e.core.HandleTombstone(t.Context(), testGameID)
		assertTombstone(t, got, ok, err, deletedByTestTombstone, true)
		if diff := cmp.Diff(afterCleanup(), e.keys(t, "games/")); diff != "" {
			t.Errorf("残ったキー (-want +got):\n%s", diff)
		}
	})

	t.Run("8-X11 ゲームの tombstone があれば、何も変えずに拒否する", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		e.putTombstone(t)
		del := e.watchDeletion()

		if err := e.deleteGame(t); !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Fatalf("DeleteGame error = %v, want ErrRemoteDeleted", err)
		}
		assertNotDeleted(t, e, del)
	})

	t.Run("8-X11 バックアップの間にゲームの tombstone が書かれたら、リモートを削除せずに止める", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		del := e.watchDeletion()
		e.putTombstoneOnGet(2)

		if err := e.deleteGame(t); !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Fatalf("DeleteGame error = %v, want ErrRemoteDeleted", err)
		}
		if del.calls != 0 || !e.registered(t) {
			t.Errorf("deleted.json の PUT %d 回・登録 %v, want 0 回・残る", del.calls, e.registered(t))
		}
		assertLockReleased(t, e)
	})

	t.Run("8-X11 ゲームの tombstone を確かめられなければ、何も変えずに止める", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		injected := errors.New("injected: get deleted.json")
		failOn(e, memstore.OpGet, "deleted.json", injected)
		del := e.watchDeletion()

		err := e.deleteGame(t)
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
		del := e.watchDeletion()

		res, err := e.core.DeleteGame(t.Context(), testGameID)
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
			t.Errorf("deleted.json の PUT %d 回・登録 %v, want 1 回・解除", del.calls, e.registered(t))
		}
	})

	t.Run("8-X13 実行ファイルがなければ、実行中でないとして削除する", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		if err := os.Remove(e.exePath); err != nil {
			t.Fatalf("remove exe: %v", err)
		}
		del := e.watchDeletion()

		if err := e.deleteGame(t); err != nil {
			t.Fatalf("DeleteGame: %v", err)
		}
		if del.calls != 1 || e.registered(t) {
			t.Errorf("deleted.json の PUT %d 回・登録 %v, want 1 回・解除", del.calls, e.registered(t))
		}
	})

	t.Run("8章/ゲームの削除/バックアップのコピーに失敗したら、リモートに触れず、途中までのコピーを残さない", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		// 通常のファイルでないものはコピーできない（copyTree）
		if err := os.Symlink("slot1.sav", filepath.Join(e.saveDir, "link.sav")); err != nil {
			t.Skipf("symlink を作れない: %v", err)
		}
		del := e.watchDeletion()

		if err := e.deleteGame(t); err == nil {
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
		del := e.watchDeletion()

		if err := e.deleteGame(t); err == nil {
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
			{"BackupsDir", func(cfg *syncore.Config) { cfg.BackupsDir = "" }},
			{"Rename", func(cfg *syncore.Config) { cfg.Rename = nil }},
			{"DeviceName", func(cfg *syncore.Config) { cfg.DeviceName = "" }},
			{"Remote", func(cfg *syncore.Config) { cfg.Remote = nil }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				e := newEnv(t)
				writeFiles(t, e.saveDir, files)
				del := e.watchDeletion()

				if _, err := e.coreWithout(tc.edit).DeleteGame(t.Context(), testGameID); err == nil {
					t.Fatal("エラーを返さなかった")
				}
				if acquired, _ := e.lock.state(); acquired != 0 {
					t.Errorf("同期ロックを %d 回取った", acquired)
				}
				assertNotDeleted(t, e, del)
			})
		}
	})
}

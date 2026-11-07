package syncore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

// backupNamePattern は、testNow に Pull を始めたときのバックアップの名前に当たる。
var backupNamePattern = regexp.MustCompile(`^20261103T010203\.004Z-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// pullOverLocal は、local のファイルをセーブフォルダに置いて base にし、remote を新しい世代として置いて、判定が Pull の plan を返す。
func pullOverLocal(t *testing.T, e env, local, remote map[string]string) syncore.Plan {
	t.Helper()
	writeFiles(t, e.saveDir, local)
	e.setBase(t, syncore.ManifestHash(entriesOf(local)))
	return approvedPull(t, e, remote)
}

// failRenameOldToBackups は、old をバックアップへリネームで移すことだけを失敗させる。
func failRenameOldToBackups(e env) {
	e.rename.fn = func(oldpath, newpath string) error {
		if strings.HasPrefix(filepath.Base(oldpath), ".cloudlaunch-old-") && strings.HasPrefix(newpath, e.backupsDir) {
			return errors.New("injected: cross-device")
		}
		return nil
	}
}

func mkdirBackups(t *testing.T, e env, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(e.backupsDir, testGameID, name), 0o700); err != nil {
			t.Fatalf("mkdir backup: %v", err)
		}
	}
}

// assertBackedUp は、Pull の後に、セーブフォルダが remote のファイルになり、old とジャーナルが残らず、
// local のファイルを持つバックアップが 1 つできたことを確かめる。
func assertBackedUp(t *testing.T, e env, local, remote map[string]string) {
	t.Helper()
	if diff := cmp.Diff(remote, readFiles(t, e.saveDir)); diff != "" {
		t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
	}
	if dirs := e.workDirs(t, "old"); len(dirs) != 0 {
		t.Errorf("old が残っている: %v", dirs)
	}
	if n := e.journalCount(t); n != 0 {
		t.Errorf("ジャーナルの行の数 = %d, want 0", n)
	}
	names := e.gameBackups(t)
	if len(names) != 1 {
		t.Fatalf("バックアップ = %v, want 1 つ", names)
	}
	if diff := cmp.Diff(local, readFiles(t, filepath.Join(e.backupsDir, testGameID, names[0]))); diff != "" {
		t.Errorf("バックアップ (-want +got):\n%s", diff)
	}
}

func TestPullバックアップ(t *testing.T) {
	local := map[string]string{"slot1.sav": "old one", "dir/slot2.sav": "old two"}
	remote := map[string]string{"slot1.sav": "new one"}

	t.Run("6-L9 old がある → バックアップへ移し、ジャーナルを消す", func(t *testing.T) {
		e := newEnv(t)
		plan := pullOverLocal(t, e, local, remote)

		if err := e.core.Pull(t.Context(), plan); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		assertBackedUp(t, e, local, remote)
	})

	t.Run("6-L9 リネームで移せない → コピーしてバックアップへ移す", func(t *testing.T) {
		e := newEnv(t)
		plan := pullOverLocal(t, e, local, remote)
		failRenameOldToBackups(e)

		if err := e.core.Pull(t.Context(), plan); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		assertBackedUp(t, e, local, remote)
	})

	t.Run("6-L9 バックアップへ移せない → old とジャーナルを残してエラー", func(t *testing.T) {
		e := newEnv(t)
		plan := pullOverLocal(t, e, local, remote)
		failRenameOldToBackups(e)
		// バックアップの置き場所をファイルにして、コピーの先も作れないようにする
		if err := os.WriteFile(e.backupsDir, nil, 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}

		if err := e.core.Pull(t.Context(), plan); err == nil {
			t.Fatal("Pull error = nil, want an error")
		}
		if diff := cmp.Diff(remote, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
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
		if got, want := e.base(t), syncore.ManifestHash(entriesOf(remote)); got != want {
			t.Errorf("base = %q, want %q", got, want)
		}
	})

	t.Run("6-L9 バックアップが 3 個を超える → 名前の古いものから削除する", func(t *testing.T) {
		e := newEnv(t)
		mkdirBackups(t, e,
			"20261101T000000.000Z-0192d4e8-0000-7000-8000-000000000101",
			"20261102T000000.000Z-0192d4e8-0000-7000-8000-000000000102",
			"20261103T000000.000Z-0192d4e8-0000-7000-8000-000000000103",
		)
		plan := pullOverLocal(t, e, local, remote)

		if err := e.core.Pull(t.Context(), plan); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		names := e.gameBackups(t)
		if len(names) != 3 {
			t.Fatalf("バックアップ = %v, want 3 つ", names)
		}
		want := []string{
			"20261102T000000.000Z-0192d4e8-0000-7000-8000-000000000102",
			"20261103T000000.000Z-0192d4e8-0000-7000-8000-000000000103",
		}
		if diff := cmp.Diff(want, names[:2]); diff != "" {
			t.Errorf("残った古いバックアップ (-want +got):\n%s", diff)
		}
		if !backupNamePattern.MatchString(names[2]) {
			t.Errorf("新しいバックアップの名前 = %q", names[2])
		}
	})

	t.Run("6-L9 live がない → バックアップを作らない", func(t *testing.T) {
		e := newEnv(t)
		removeSaveDir(t, e)
		plan := approvedPull(t, e, remote)

		if err := e.core.Pull(t.Context(), plan); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if names := e.gameBackups(t); len(names) != 0 {
			t.Errorf("バックアップ = %v, want なし", names)
		}
	})

	t.Run("6-L9 入れ替えの後にキャンセルされても、手順 10 まで行う", func(t *testing.T) {
		e := newEnv(t)
		plan := pullOverLocal(t, e, local, remote)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		// old をバックアップへリネームする時点でキャンセルし、リネームを失敗させてコピーの経路に入れる
		e.rename.fn = func(oldpath, newpath string) error {
			if strings.HasPrefix(filepath.Base(oldpath), ".cloudlaunch-old-") && strings.HasPrefix(newpath, e.backupsDir) {
				cancel()
				return errors.New("injected: cross-device")
			}
			return nil
		}

		if err := e.core.Pull(ctx, plan); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		assertBackedUp(t, e, local, remote)
	})

	t.Run("6-L9 コピーが途中で失敗 → .partial を残さず、old とジャーナルを残してエラー", func(t *testing.T) {
		e := newEnv(t)
		plan := pullOverLocal(t, e, local, remote)
		// リネームを失敗させてコピーの経路に入れ、old の最後（名前の順）に通常のファイルでないものを置いて、
		// ほかのファイルをコピーした後でコピーを失敗させる
		e.rename.fn = func(oldpath, newpath string) error {
			if strings.HasPrefix(filepath.Base(oldpath), ".cloudlaunch-old-") && strings.HasPrefix(newpath, e.backupsDir) {
				if err := os.Symlink("slot1.sav", filepath.Join(oldpath, "zz-link")); err != nil {
					t.Skipf("シンボリックリンクを作れない: %v", err)
				}
				return errors.New("injected: cross-device")
			}
			return nil
		}

		if err := e.core.Pull(t.Context(), plan); err == nil {
			t.Fatal("Pull error = nil, want an error")
		}
		if names := e.gameBackups(t); len(names) != 0 {
			t.Errorf("バックアップのディレクトリに残っている: %v", names)
		}
		old := e.workDirs(t, "old")
		if len(old) != 1 {
			t.Fatalf("old = %v, want 1 つ", old)
		}
		if _, err := os.Lstat(filepath.Join(old[0], "slot1.sav")); err != nil {
			t.Errorf("old のファイルがない: %v", err)
		}
		if n := e.journalCount(t); n != 1 {
			t.Errorf("ジャーナルの行の数 = %d, want 1", n)
		}
	})

	t.Run("6章/Pull/BackupsDir が設定されていない → ジャーナルを書く前にエラー", func(t *testing.T) {
		e := newEnv(t)
		e.core = e.coreWithout(func(cfg *syncore.Config) { cfg.BackupsDir = "" })
		plan := pullOverLocal(t, e, local, remote)

		if err := e.core.Pull(t.Context(), plan); err == nil {
			t.Fatal("Pull error = nil, want an error")
		}
		if diff := cmp.Diff(local, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
		if dirs := append(e.workDirs(t, "stage"), e.workDirs(t, "old")...); len(dirs) != 0 {
			t.Errorf("作業用のディレクトリが残っている: %v", dirs)
		}
		if n := e.journalCount(t); n != 0 {
			t.Errorf("ジャーナルの行の数 = %d, want 0", n)
		}
	})

	t.Run("3章/バックアップ/その Pull で作ったバックアップは、名前が古くても削除しない", func(t *testing.T) {
		e := newEnv(t)
		// 時計が進んでいた間に作った、testNow より新しい名前のバックアップ
		future := []string{
			"20271101T000000.000Z-0192d4e8-0000-7000-8000-000000000101",
			"20271102T000000.000Z-0192d4e8-0000-7000-8000-000000000102",
			"20271103T000000.000Z-0192d4e8-0000-7000-8000-000000000103",
		}
		mkdirBackups(t, e, future...)
		plan := pullOverLocal(t, e, local, remote)

		if err := e.core.Pull(t.Context(), plan); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		names := e.gameBackups(t)
		if len(names) != 4 || !backupNamePattern.MatchString(names[0]) {
			t.Fatalf("バックアップ = %v, want 作ったものと元の 3 つ", names)
		}
		if diff := cmp.Diff(local, readFiles(t, filepath.Join(e.backupsDir, testGameID, names[0]))); diff != "" {
			t.Errorf("作ったバックアップ (-want +got):\n%s", diff)
		}
	})

	t.Run("3章/バックアップ/空のディレクトリもバックアップに入る", func(t *testing.T) {
		e := newEnv(t)
		if err := os.Mkdir(filepath.Join(e.saveDir, "empty"), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		plan := pullOverLocal(t, e, local, remote)
		failRenameOldToBackups(e)

		if err := e.core.Pull(t.Context(), plan); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		names := e.gameBackups(t)
		if len(names) != 1 {
			t.Fatalf("バックアップ = %v, want 1 つ", names)
		}
		info, err := os.Stat(filepath.Join(e.backupsDir, testGameID, names[0], "empty"))
		if err != nil || !info.IsDir() {
			t.Errorf("空のディレクトリがバックアップにない: %v", err)
		}
	})

	t.Run("3章/バックアップ/名前は Pull を始めた時刻と opId から作る", func(t *testing.T) {
		e := newEnv(t)
		plan := pullOverLocal(t, e, local, remote)

		if err := e.core.Pull(t.Context(), plan); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		names := e.gameBackups(t)
		if len(names) != 1 || !backupNamePattern.MatchString(names[0]) {
			t.Errorf("バックアップ = %v, want %s の形の 1 つ", names, backupNamePattern)
		}
	})

	t.Run("3章/バックアップ/名前の形に合わないものは数えも削除もしない", func(t *testing.T) {
		e := newEnv(t)
		valid := []string{
			"20261101T000000.000Z-0192d4e8-0000-7000-8000-000000000101",
			"20261102T000000.000Z-0192d4e8-0000-7000-8000-000000000102",
		}
		others := []string{
			".partial-20261030T000000.000Z-0192d4e8-0000-7000-8000-000000000100",
			"20261031",
			"manual-copy",
		}
		mkdirBackups(t, e, append(append([]string{}, valid...), others...)...)
		writeFiles(t, filepath.Join(e.backupsDir, testGameID), map[string]string{"memo.txt": "memo"})
		plan := pullOverLocal(t, e, local, remote)

		if err := e.core.Pull(t.Context(), plan); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		names := e.gameBackups(t)
		for _, want := range append(append([]string{"memo.txt"}, valid...), others...) {
			if !slices.Contains(names, want) {
				t.Errorf("%q が消えた: %v", want, names)
			}
		}
		if len(names) != len(valid)+len(others)+2 {
			t.Errorf("バックアップ = %v, want 元のものと新しい 1 つ", names)
		}
	})
}

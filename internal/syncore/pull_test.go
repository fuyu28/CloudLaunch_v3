package syncore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

const otherGeneration = "0192d4e8-0000-7000-8000-0000000000cc"

// approvedPull は、files の object と世代をリモートに置いて Status を呼び、判定が Pull の plan を返す。
func approvedPull(t *testing.T, e env, files map[string]string) syncore.Plan {
	t.Helper()
	for _, content := range files {
		e.putObject(t, objectKey(content), content)
	}
	e.putGeneration(t, entriesOf(files))
	plan, err := e.status(t)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if plan.Decision != syncore.DecisionPull {
		t.Fatalf("Decision = %v, want Pull", plan.Decision)
	}
	return plan
}

func removeSaveDir(t *testing.T, e env) {
	t.Helper()
	if err := os.Remove(e.saveDir); err != nil {
		t.Fatalf("remove save dir: %v", err)
	}
}

func failGet(e env, keyPart string, err error) {
	e.remote.Before = func(op memstore.Op, key string) error {
		if op == memstore.OpGet && strings.Contains(key, keyPart) {
			return err
		}
		return nil
	}
}

// assertAborted は、中止した後に stage とジャーナルが残っておらず、セーブフォルダと base が同期前のままであることを確かめる。
func assertAborted(t *testing.T, e env, wantFiles map[string]string) {
	t.Helper()
	if dirs := e.workDirs(t, "stage"); len(dirs) != 0 {
		t.Errorf("stage が残っている: %v", dirs)
	}
	if dirs := e.workDirs(t, "old"); len(dirs) != 0 {
		t.Errorf("old ができた: %v", dirs)
	}
	if n := e.journalCount(t); n != 0 {
		t.Errorf("ジャーナルの行の数 = %d, want 0", n)
	}
	if diff := cmp.Diff(wantFiles, readFiles(t, e.saveDir)); diff != "" {
		t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
	}
	if base := e.base(t); base != "" {
		t.Errorf("base = %q, want 空", base)
	}
}

func TestPull(t *testing.T) {
	files := map[string]string{"slot1.sav": "one", "dir/slot2.sav": "two"}

	t.Run("6-L7 manifest のファイルをセーブフォルダに取り込む", func(t *testing.T) {
		e := newEnv(t)
		removeSaveDir(t, e)
		plan := approvedPull(t, e, files)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if diff := cmp.Diff(files, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
	})

	t.Run("6-L4 同じ中身の 2 つのファイルをどちらも取り込む", func(t *testing.T) {
		e := newEnv(t)
		removeSaveDir(t, e)
		same := map[string]string{"a.sav": "same", "b.sav": "same"}
		plan := approvedPull(t, e, same)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if diff := cmp.Diff(same, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
	})

	t.Run("6-L7 空 manifest の世代を取り込むと、空のセーブフォルダができる", func(t *testing.T) {
		e := newEnv(t)
		removeSaveDir(t, e)
		plan := approvedPull(t, e, map[string]string{})

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		info, err := os.Stat(e.saveDir)
		if err != nil || !info.IsDir() {
			t.Fatalf("セーブフォルダがない: %v", err)
		}
		if got := readFiles(t, e.saveDir); len(got) != 0 {
			t.Errorf("セーブフォルダのファイル = %v, want なし", got)
		}
	})

	t.Run("6章/Pull/セーブフォルダの親ディレクトリがなければ作る", func(t *testing.T) {
		e := newEnv(t)
		removeSaveDir(t, e)
		plan := approvedPull(t, e, files)
		parent := filepath.Join(filepath.Dir(e.saveDir), "missing", "parent")
		e.saveDir = filepath.Join(parent, "save")
		e.execDB(t, `UPDATE game_locals SET save_folder_path = ? WHERE game_id = ?`, e.saveDir, testGameID)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if diff := cmp.Diff(files, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
	})

	t.Run("6-L8 base を remote の manifest ハッシュにする", func(t *testing.T) {
		e := newEnv(t)
		removeSaveDir(t, e)
		plan := approvedPull(t, e, files)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if got, want := e.base(t), syncore.ManifestHash(entriesOf(files)); got != want {
			t.Errorf("base = %q, want %q", got, want)
		}
	})

	t.Run("6-L10 終わったら stage を残さず、ジャーナルを削除する", func(t *testing.T) {
		e := newEnv(t)
		removeSaveDir(t, e)
		plan := approvedPull(t, e, files)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if dirs := e.workDirs(t, "stage"); len(dirs) != 0 {
			t.Errorf("stage が残っている: %v", dirs)
		}
		if dirs := e.workDirs(t, "old"); len(dirs) != 0 {
			t.Errorf("old ができた: %v", dirs)
		}
		if n := e.journalCount(t); n != 0 {
			t.Errorf("ジャーナルの行の数 = %d, want 0", n)
		}
	})

	t.Run("6章/Pull/判定が Pull でない plan はエラー", func(t *testing.T) {
		e := newEnv(t)
		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if plan.Decision != syncore.DecisionNoChange {
			t.Fatalf("Decision = %v, want 変更なし", plan.Decision)
		}

		err = e.core.Pull(t.Context(), plan, syncore.PullOptions{})
		if err == nil || errors.Is(err, syncore.ErrPlanStale) {
			t.Fatalf("Pull error = %v, want an error other than ErrPlanStale", err)
		}
		assertAborted(t, e, map[string]string{})
	})
}

func TestPull中止(t *testing.T) {
	files := map[string]string{"slot1.sav": "one", "slot2.sav": "two"}
	injected := errors.New("injected")

	t.Run("6-L4 object がなければ中止し、stage とジャーナルを削除する", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, files)
		if err := e.remote.Delete(t.Context(), objectKey("two")); err != nil {
			t.Fatalf("delete: %v", err)
		}

		err := e.core.Pull(t.Context(), plan, syncore.PullOptions{})
		if err == nil || errors.Is(err, syncore.ErrPlanStale) {
			t.Fatalf("Pull error = %v, want an error other than ErrPlanStale", err)
		}
		assertAborted(t, e, map[string]string{})
	})

	t.Run("6-A1 object の取得が失敗したら中止し、stage とジャーナルを削除する", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, files)
		failGet(e, "/objects/", injected)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); !errors.Is(err, injected) {
			t.Fatalf("Pull error = %v, want %v", err, injected)
		}
		assertAborted(t, e, map[string]string{})
	})

	t.Run("6-A1 キャンセルで中止しても、stage とジャーナルを削除する", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, files)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpGet && strings.Contains(key, "/objects/") {
				cancel()
				return context.Canceled
			}
			return nil
		}

		if err := e.core.Pull(ctx, plan, syncore.PullOptions{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Pull error = %v, want context.Canceled", err)
		}
		assertAborted(t, e, map[string]string{})
	})

	t.Run("6-L5 stage が manifest と一致しなければ中止し、stage とジャーナルを削除する", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, files)
		e.putObject(t, objectKey("two"), "broken")

		err := e.core.Pull(t.Context(), plan, syncore.PullOptions{})
		if err == nil || errors.Is(err, syncore.ErrPlanStale) {
			t.Fatalf("Pull error = %v, want an error other than ErrPlanStale", err)
		}
		assertAborted(t, e, map[string]string{})
	})

	t.Run("6-L6 承認後にセーブフォルダが変わっていたら ErrPlanStale、stage とジャーナルを削除する", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, files)
		changed := map[string]string{"slot1.sav": "local"}
		writeFiles(t, e.saveDir, changed)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); !errors.Is(err, syncore.ErrPlanStale) {
			t.Fatalf("Pull error = %v, want ErrPlanStale", err)
		}
		assertAborted(t, e, changed)
	})

	t.Run("6-L6 承認後に HEAD が変わっていたら ErrPlanStale、stage とジャーナルを削除する", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, files)
		e.putManifest(t, otherGeneration, entriesOf(map[string]string{"slot1.sav": "one"}))
		e.putObject(t, headKey(), otherGeneration)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); !errors.Is(err, syncore.ErrPlanStale) {
			t.Fatalf("Pull error = %v, want ErrPlanStale", err)
		}
		assertAborted(t, e, map[string]string{})
	})

	t.Run("6-L6 承認後に HEAD が消えていたら ErrPlanStale、stage とジャーナルを削除する", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPull(t, e, files)
		if err := e.remote.Delete(t.Context(), headKey()); err != nil {
			t.Fatalf("delete: %v", err)
		}

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); !errors.Is(err, syncore.ErrPlanStale) {
			t.Fatalf("Pull error = %v, want ErrPlanStale", err)
		}
		assertAborted(t, e, map[string]string{})
	})
}

func TestPull空manifestのbase(t *testing.T) {
	files := map[string]string{"slot1.sav": "one"}
	emptyHash := syncore.ManifestHash(nil)

	t.Run("6-L6 base が空 manifest で、セーブフォルダが空 → 変わっていないとして取り込む", func(t *testing.T) {
		e := newEnv(t)
		e.setBase(t, emptyHash)
		plan := approvedPull(t, e, files)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if diff := cmp.Diff(files, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
	})

	t.Run("6-L6 base が空 manifest で、セーブフォルダがない → 変わっていないとして取り込む", func(t *testing.T) {
		e := newEnv(t)
		removeSaveDir(t, e)
		e.setBase(t, emptyHash)
		plan := approvedPull(t, e, files)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if diff := cmp.Diff(files, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
	})

	t.Run("6-L6 base が空 manifest で、承認後にファイルができた → ErrPlanStale", func(t *testing.T) {
		e := newEnv(t)
		e.setBase(t, emptyHash)
		plan := approvedPull(t, e, files)
		changed := map[string]string{"slot1.sav": "local"}
		writeFiles(t, e.saveDir, changed)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); !errors.Is(err, syncore.ErrPlanStale) {
			t.Fatalf("Pull error = %v, want ErrPlanStale", err)
		}
		if diff := cmp.Diff(changed, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
		if n := e.journalCount(t); n != 0 {
			t.Errorf("ジャーナルの行の数 = %d, want 0", n)
		}
		if got := e.base(t); got != emptyHash {
			t.Errorf("base = %q, want 空 manifest のハッシュ", got)
		}
	})
}

func TestPull不正なmanifest(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
	}{
		{"6-M1 path が空", []string{""}},
		{"6-M2 「/」で始まる", []string{"/etc/passwd"}},
		{"6-M2 「\\」で始まる", []string{`\Windows\win.ini`}},
		{"6-M2 ドライブレターを含む", []string{"C:/Windows/win.ini"}},
		{"6-M2 要素の途中にドライブレターを含む", []string{"a/C:b.sav"}},
		{"6-M2 UNC パス", []string{`\\server\share\a.sav`}},
		{"6-M3 .. の要素がある", []string{"../a.sav"}},
		{"6-M3 途中に .. の要素がある", []string{"dir/../../a.sav"}},
		{"6-M3 . の要素がある", []string{"./a.sav"}},
		{"6-M3 空の要素がある", []string{"dir//a.sav"}},
		{"6-M3 末尾が「/」で空の要素がある", []string{"dir/"}},
		{"6-M4 「\\」を含む", []string{`dir\a.sav`}},
		{"6-M5 大文字小文字の違いだけで重複する", []string{"Slot1.sav", "slot1.sav"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			entries := make([]syncore.Entry, len(tt.paths))
			for i, p := range tt.paths {
				entries[i] = syncore.Entry{Path: p, SHA256: sha256Hex("x"), Size: 1}
			}
			e.putObject(t, objectKey("x"), "x")
			e.putGeneration(t, entries)
			plan, err := e.status(t)
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			var gets atomic.Int32
			e.remote.Before = func(op memstore.Op, key string) error {
				if op == memstore.OpGet && strings.Contains(key, "/objects/") {
					gets.Add(1)
				}
				return nil
			}

			err = e.core.Pull(t.Context(), plan, syncore.PullOptions{})
			if err == nil || errors.Is(err, syncore.ErrPlanStale) {
				t.Fatalf("Pull error = %v, want an error other than ErrPlanStale", err)
			}
			if n := gets.Load(); n != 0 {
				t.Errorf("object を %d 回取得した", n)
			}
			assertAborted(t, e, map[string]string{})
		})
	}

	t.Run("6-M3 要素の一部に .. を含むだけなら正当", func(t *testing.T) {
		e := newEnv(t)
		removeSaveDir(t, e)
		files := map[string]string{"a..b.sav": "one", "dir/..c": "two"}
		plan := approvedPull(t, e, files)

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if diff := cmp.Diff(files, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
	})
}

func TestPull複数PC(t *testing.T) {
	t.Run("6章/複数 PC/Push したセーブを、別の PC が Pull で取り込む", func(t *testing.T) {
		files := map[string]string{"slot1.sav": "one", "dir/slot2.sav": "two"}
		a := newEnv(t)
		b := newEnvWithRemote(t, a.remote)
		removeSaveDir(t, b)
		if err := a.core.Push(t.Context(), approvedPush(t, a, files), syncore.PushOptions{}); err != nil {
			t.Fatalf("Push: %v", err)
		}

		plan, err := b.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if plan.Decision != syncore.DecisionPull {
			t.Fatalf("Decision = %v, want Pull", plan.Decision)
		}
		if err := b.core.Pull(t.Context(), plan, syncore.PullOptions{}); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if diff := cmp.Diff(files, readFiles(t, b.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
		if got, want := b.base(t), a.base(t); got != want {
			t.Errorf("base = %q, want %q（Push した PC の base）", got, want)
		}
	})
}

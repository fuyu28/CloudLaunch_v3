package syncore_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

func Test確認ダイアログの情報(t *testing.T) {
	// a は local だけ、b は中身が違い、c は同じ、d は remote だけにある
	local := map[string]string{"a.sav": "added", "b.sav": "local-b", "c.sav": "same"}
	remote := map[string]string{"b.sav": "remote-bb", "c.sav": "same", "d.sav": "deleted"}

	changesOf := func(t *testing.T, d syncore.Direction) syncore.Changes {
		t.Helper()
		e := newEnv(t)
		writeFiles(t, e.saveDir, local)
		e.putGeneration(t, entriesOf(remote))
		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		return plan.Changes(d)
	}

	t.Run("7章/確認ダイアログ/Push では、local にあって remote にないファイルを追加、中身の違うファイルを変更、remote にだけあるファイルを削除と数える", func(t *testing.T) {
		got := changesOf(t, syncore.DirectionUpload)
		want := syncore.Changes{Added: 1, Modified: 1, Deleted: 1, Bytes: int64(len("added") + len("local-b"))}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Changes(Upload) (-want +got):\n%s", diff)
		}
	})

	t.Run("7章/確認ダイアログ/Pull では、remote を基準に同じく数える", func(t *testing.T) {
		got := changesOf(t, syncore.DirectionDownload)
		want := syncore.Changes{Added: 1, Modified: 1, Deleted: 1, Bytes: int64(len("deleted") + len("remote-bb"))}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Changes(Download) (-want +got):\n%s", diff)
		}
	})

	t.Run("7章/確認ダイアログ/転送するサイズは、追加と変更のファイルの大きさの合計にする", func(t *testing.T) {
		got := changesOf(t, syncore.DirectionUpload)
		// 同じ c.sav と、削除する d.sav は数えない
		if want := int64(len("added") + len("local-b")); got.Bytes != want {
			t.Errorf("Bytes = %d, want %d", got.Bytes, want)
		}
	})

	t.Run("7章/確認ダイアログ/local の最終更新日時は、ファイルの mtime の最大値にする", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, map[string]string{"old.sav": "old", "dir/new.sav": "new"})
		older := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		newer := time.Date(2026, 6, 7, 8, 9, 10, 0, time.UTC)
		for rel, mtime := range map[string]time.Time{"old.sav": older, "dir/new.sav": newer} {
			if err := os.Chtimes(filepath.Join(e.saveDir, filepath.FromSlash(rel)), mtime, mtime); err != nil {
				t.Fatalf("chtimes %s: %v", rel, err)
			}
		}

		plan := e.planWant(t, syncore.DecisionPush)
		if !plan.Local.ModTime.Equal(newer) {
			t.Errorf("Local.ModTime = %v, want %v", plan.Local.ModTime, newer)
		}
	})

	t.Run("7章/確認ダイアログ/local にファイルがなければ、最終更新日時を持たない", func(t *testing.T) {
		e := newEnv(t)
		e.putGeneration(t, entriesOf(remote))

		plan := e.planWant(t, syncore.DecisionPull)
		if !plan.Local.ModTime.IsZero() {
			t.Errorf("Local.ModTime = %v, want ゼロ値", plan.Local.ModTime)
		}
	})
}

package syncore_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

// oldGeneration は、pushGeneration より古い i 番目の世代のキー。i が大きいほど新しい。
func oldGeneration(i int) string {
	return fmt.Sprintf("0192d4e7-0000-7000-8000-%012x", i)
}

// genContent は、i 番目の古い世代だけが参照する object の中身。
func genContent(i int) string { return fmt.Sprintf("v%d", i) }

// putOldGenerations は、1〜n 番目の世代を置き、HEAD を n 番目に向ける。i 番目の世代は slot.sav に genContent(i) を持ち、
// extra[i] があればそのファイルも持つ。
func putOldGenerations(t *testing.T, e env, n int, extra map[int]map[string]string) {
	t.Helper()
	for i := 1; i <= n; i++ {
		files := map[string]string{"slot.sav": genContent(i)}
		for path, content := range extra[i] {
			files[path] = content
		}
		for _, content := range files {
			e.putObject(t, objectKey(content), content)
		}
		e.putManifest(t, oldGeneration(i), entriesOf(files))
	}
	e.putObject(t, headKey(), oldGeneration(n))
}

// pushAfterOld は、n 番目の世代を base にして local を書き換え、判定が Push の plan で Push する。
func pushAfterOld(t *testing.T, e env, core *syncore.Core, ctx context.Context, n int) {
	t.Helper()
	e.setBase(t, syncore.ManifestHash(entriesOf(map[string]string{"slot.sav": genContent(n)})))
	plan := approvedPush(t, e, map[string]string{"slot.sav": "new"})
	if err := core.Push(ctx, plan, syncore.PushOptions{}); err != nil {
		t.Fatalf("Push: %v", err)
	}
}

func oldManifestKeys(from, to int) []string {
	keys := []string{}
	for i := from; i <= to; i++ {
		keys = append(keys, manifestKey(oldGeneration(i)))
	}
	return keys
}

// assertPushed は、GC がどうなっても、Push が HEAD と base を更新したことを確かめる（仕様書 5-P8）。
func assertPushed(t *testing.T, e env) {
	t.Helper()
	if head, _ := e.getObject(t, headKey()); head != pushGeneration {
		t.Errorf("HEAD = %q, want %q", head, pushGeneration)
	}
	if got, want := e.base(t), syncore.ManifestHash(entriesOf(map[string]string{"slot.sav": "new"})); got != want {
		t.Errorf("base = %q, want %q", got, want)
	}
}

func (e env) hasObject(t *testing.T, content string) bool {
	t.Helper()
	_, ok := e.getObject(t, objectKey(content))
	return ok
}

func TestPushGC(t *testing.T) {
	t.Run("2章/世代の保持と GC/新しい 10 個の世代を残し、古い世代を削除する", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 11, nil)

		pushAfterOld(t, e, e.core, t.Context(), 11)

		want := append(oldManifestKeys(3, 11), manifestKey(pushGeneration))
		if diff := cmp.Diff(want, e.keys(t, manifestsPrefix)); diff != "" {
			t.Errorf("manifests (-want +got):\n%s", diff)
		}
	})

	t.Run("2章/世代の保持と GC/世代が 10 個以下なら削除しない", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 9, nil)

		pushAfterOld(t, e, e.core, t.Context(), 9)

		want := append(oldManifestKeys(1, 9), manifestKey(pushGeneration))
		if diff := cmp.Diff(want, e.keys(t, manifestsPrefix)); diff != "" {
			t.Errorf("manifests (-want +got):\n%s", diff)
		}
		if !e.hasObject(t, genContent(1)) {
			t.Error("最も古い世代の object を削除した")
		}
	})

	t.Run("2章/世代の保持と GC/HEAD が指す世代は、新しい 10 個に入らなくても残す", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 11, nil)
		// 時計の遅れた PC が Push すると、新しい世代のキーが既存のどれよりも古い順に並ぶ
		skewed := "0192d4e6-0000-7000-8000-000000000001"
		core := e.coreWithout(func(cfg *syncore.Config) {
			cfg.NewGeneration = func() (string, error) { return skewed, nil }
		})

		pushAfterOld(t, e, core, t.Context(), 11)

		want := append([]string{manifestKey(skewed)}, oldManifestKeys(2, 11)...)
		if diff := cmp.Diff(want, e.keys(t, manifestsPrefix)); diff != "" {
			t.Errorf("manifests (-want +got):\n%s", diff)
		}
		if !e.hasObject(t, "new") {
			t.Error("HEAD が指す世代の object を削除した")
		}
	})

	t.Run("2章/世代の保持と GC/残した世代のどれからも参照されない object だけを削除する", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 11, map[int]map[string]string{
			1: {"shared.sav": "shared"},
			5: {"shared.sav": "shared"},
		})
		e.putObject(t, objectKey("orphan"), "orphan")

		pushAfterOld(t, e, e.core, t.Context(), 11)

		for content, want := range map[string]bool{
			genContent(1): false, genContent(2): false, "orphan": false,
			genContent(3): true, genContent(11): true, "shared": true, "new": true,
		} {
			if got := e.hasObject(t, content); got != want {
				t.Errorf("object %q がある = %v, want %v", content, got, want)
			}
		}
	})

	t.Run("2章/世代の保持と GC/想定しない名前のキーは、数えも消しもしない", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 10, nil)
		odd := []string{
			manifestsPrefix + "notes.txt",
			manifestsPrefix + "not-a-uuid.json",
			"games/" + testGameID + "/objects/README",
			"games/" + testGameID + "/objects/" + strings.ToUpper(sha256Hex("upper")),
		}
		for _, key := range odd {
			e.putObject(t, key, "odd")
		}

		pushAfterOld(t, e, e.core, t.Context(), 10)

		for _, key := range odd {
			if _, ok := e.getObject(t, key); !ok {
				t.Errorf("%s を削除した", key)
			}
		}
		if _, ok := e.getObject(t, manifestKey(oldGeneration(1))); ok {
			t.Error("世代が 11 個あるのに、最も古い世代を残した（想定しない名前のキーを数えた）")
		}
		if _, ok := e.getObject(t, manifestKey(oldGeneration(2))); !ok {
			t.Error("2 番目に古い世代を削除した")
		}
	})

	t.Run("2章/世代の保持と GC/残す世代の manifest が読めなければ、object を削除しない", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 11, nil)
		e.putObject(t, manifestKey(oldGeneration(5)), "{not json")
		e.putObject(t, objectKey("orphan"), "orphan")

		pushAfterOld(t, e, e.core, t.Context(), 11)

		assertPushed(t, e)
		if !e.hasObject(t, "orphan") || !e.hasObject(t, genContent(1)) {
			t.Error("参照を確かめられないのに object を削除した")
		}
	})

	t.Run("2章/世代の保持と GC/GC を始めたときに HEAD が指している世代は、Push した世代でなくても残す", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 11, nil)
		// Push が HEAD を書いた後、GC が世代を一覧する前に、時計の遅れた別の PC が HEAD を書き換えたものとする
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpList && strings.HasSuffix(key, "/manifests/") {
				e.remote.Before = nil
				e.putObject(t, headKey(), oldGeneration(1))
			}
			return nil
		}

		pushAfterOld(t, e, e.core, t.Context(), 11)

		want := append([]string{manifestKey(oldGeneration(1))}, oldManifestKeys(3, 11)...)
		want = append(want, manifestKey(pushGeneration))
		if diff := cmp.Diff(want, e.keys(t, manifestsPrefix)); diff != "" {
			t.Errorf("manifests (-want +got):\n%s", diff)
		}
		if !e.hasObject(t, genContent(1)) {
			t.Error("HEAD が指す世代の object を削除した")
		}
	})

	t.Run("2章/GC と同時 Push/GC が object を一覧するときに書かれた世代の object は削除しない", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 11, nil)
		// GC が object を一覧する直前に、別の PC が object をアップロードし、それを参照する世代を書いたものとする。
		// 世代を先に一覧すると、その世代を見落として object を消す。object の一覧は、Push の 5-P3 と GC で 2 回呼ばれる
		lists := 0
		e.remote.Before = func(op memstore.Op, key string) error {
			if op != memstore.OpList || !strings.HasSuffix(key, "/objects/") {
				return nil
			}
			lists++
			if lists == 2 {
				e.remote.Before = nil
				e.putObject(t, objectKey("concurrent"), "concurrent")
				e.putManifest(t, otherGeneration, entriesOf(map[string]string{"slot.sav": "concurrent"}))
			}
			return nil
		}

		pushAfterOld(t, e, e.core, t.Context(), 11)

		if !e.hasObject(t, "concurrent") {
			t.Error("別の PC の世代が参照する object を削除した")
		}
	})

	t.Run("2章/世代の保持と GC/残す世代の manifest を取得できなければ、object を削除しない", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 11, nil)
		e.putObject(t, objectKey("orphan"), "orphan")
		failOn(e, memstore.OpGet, manifestKey(oldGeneration(5)), errors.New("injected"))

		pushAfterOld(t, e, e.core, t.Context(), 11)

		assertPushed(t, e)
		if !e.hasObject(t, "orphan") {
			t.Error("参照を確かめられないのに object を削除した")
		}
	})

	t.Run("5-P8 object の一覧に失敗しても、Push は成功し、object を削除しない", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 11, nil)
		e.putObject(t, objectKey("orphan"), "orphan")
		// 1 回目は Push の 5-P3、2 回目が GC の一覧
		lists := 0
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpList && strings.HasSuffix(key, "/objects/") {
				lists++
				if lists == 2 {
					return errors.New("injected")
				}
			}
			return nil
		}

		pushAfterOld(t, e, e.core, t.Context(), 11)

		assertPushed(t, e)
		if !e.hasObject(t, "orphan") {
			t.Error("object を削除した")
		}
	})

	t.Run("2章/世代の保持と GC/世代の削除に失敗したら、object を削除しない", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 11, nil)
		e.putObject(t, objectKey("orphan"), "orphan")
		failOn(e, memstore.OpDelete, "/manifests/", errors.New("injected"))

		pushAfterOld(t, e, e.core, t.Context(), 11)

		assertPushed(t, e)
		if !e.hasObject(t, "orphan") || !e.hasObject(t, genContent(1)) {
			t.Error("世代を削除できなかったのに object を削除した")
		}
	})

	t.Run("5-P8 世代の一覧に失敗しても、Push は成功し、HEAD と base を更新する", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 11, nil)
		failOn(e, memstore.OpList, "/manifests/", errors.New("injected"))

		pushAfterOld(t, e, e.core, t.Context(), 11)

		assertPushed(t, e)
	})

	t.Run("5-P8 削除に失敗しても、Push は成功する", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 11, nil)
		failOn(e, memstore.OpDelete, "/objects/", errors.New("injected"))

		pushAfterOld(t, e, e.core, t.Context(), 11)

		assertPushed(t, e)
	})

	t.Run("5-P8 GC の途中でキャンセルされても、Push は成功する", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 11, nil)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		// 世代の一覧を取った直後にキャンセルされたものとする
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpList && strings.HasSuffix(key, "/manifests/") {
				cancel()
			}
			return nil
		}

		pushAfterOld(t, e, e.core, ctx, 11)

		assertPushed(t, e)
		if _, ok := e.getObject(t, manifestKey(oldGeneration(1))); !ok {
			t.Error("キャンセルされた後に世代を削除した")
		}
	})

	t.Run("5-I3 8 で中断して残った古い世代と object は、次の Push で消える", func(t *testing.T) {
		e := newEnv(t)
		// 前の Push が手順 8 の前に中断し、保持数を超えた世代と、どこからも参照されない object が残っている
		putOldGenerations(t, e, 12, nil)
		e.putObject(t, objectKey("orphan"), "orphan")

		pushAfterOld(t, e, e.core, t.Context(), 12)

		want := append(oldManifestKeys(4, 12), manifestKey(pushGeneration))
		if diff := cmp.Diff(want, e.keys(t, manifestsPrefix)); diff != "" {
			t.Errorf("manifests (-want +got):\n%s", diff)
		}
		for _, content := range []string{genContent(1), genContent(3), "orphan"} {
			if e.hasObject(t, content) {
				t.Errorf("object %q が残っている", content)
			}
		}
	})

	t.Run("7章/上書き/上書きの Push でも GC を行う", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 11, nil)
		writeFiles(t, e.saveDir, map[string]string{"slot.sav": "new"})
		plan := e.planWant(t, syncore.DecisionConflict)

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{Overwrite: true}); err != nil {
			t.Fatalf("Push: %v", err)
		}
		want := append(oldManifestKeys(3, 11), manifestKey(pushGeneration))
		if diff := cmp.Diff(want, e.keys(t, manifestsPrefix)); diff != "" {
			t.Errorf("manifests (-want +got):\n%s", diff)
		}
	})

	t.Run("7-O5 上書きで local と remote が同じなら、転送しないので GC も行わない", func(t *testing.T) {
		e := newEnv(t)
		putOldGenerations(t, e, 11, nil)
		e.putObject(t, objectKey("orphan"), "orphan")
		writeFiles(t, e.saveDir, map[string]string{"slot.sav": genContent(11)})
		plan := e.planWant(t, syncore.DecisionNoChange)

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{Overwrite: true}); err != nil {
			t.Fatalf("Push: %v", err)
		}
		if diff := cmp.Diff(oldManifestKeys(1, 11), e.keys(t, manifestsPrefix)); diff != "" {
			t.Errorf("manifests (-want +got):\n%s", diff)
		}
		if !e.hasObject(t, "orphan") {
			t.Error("object を削除した")
		}
	})
}

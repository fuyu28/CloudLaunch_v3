package syncore_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

// approvedPush は、files をセーブフォルダに置いて Status を呼び、判定が Push の plan を返す。
func approvedPush(t *testing.T, e env, files map[string]string) syncore.Plan {
	t.Helper()
	writeFiles(t, e.saveDir, files)
	plan, err := e.status(t)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if plan.Decision != syncore.DecisionPush {
		t.Fatalf("Decision = %v, want Push", plan.Decision)
	}
	return plan
}

// recordObjectPuts は、Push が objects/ に PUT したキーを記録する。Push は並列に PUT するので、ロックを取って記録する。
func recordObjectPuts(e env) func() []string {
	var mu sync.Mutex
	var keys []string
	e.remote.Before = func(op memstore.Op, key string) error {
		if op == memstore.OpPut && strings.Contains(key, "/objects/") {
			mu.Lock()
			keys = append(keys, key)
			mu.Unlock()
		}
		return nil
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return keys
	}
}

func failPut(e env, keyPart string, err error) {
	e.remote.Before = func(op memstore.Op, key string) error {
		if op == memstore.OpPut && strings.Contains(key, keyPart) {
			return err
		}
		return nil
	}
}

func TestPush(t *testing.T) {
	files := map[string]string{"slot1.sav": "one", "dir/slot2.sav": "two"}

	t.Run("5章/Push/リモートにない object だけをアップロードする", func(t *testing.T) {
		e := newEnv(t)
		e.putObject(t, objectKey("one"), "one")
		plan := approvedPush(t, e, files)
		puts := recordObjectPuts(e)

		if err := e.core.Push(t.Context(), plan); err != nil {
			t.Fatalf("Push: %v", err)
		}
		if diff := cmp.Diff([]string{objectKey("two")}, puts()); diff != "" {
			t.Errorf("PUT した object (-want +got):\n%s", diff)
		}
		if body, _ := e.getObject(t, objectKey("two")); body != "two" {
			t.Errorf("object の中身 = %q, want %q", body, "two")
		}
	})

	t.Run("5章/Push/同じ中身のファイルは object を 1 つだけアップロードする", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, map[string]string{"a.sav": "same", "b.sav": "same"})
		puts := recordObjectPuts(e)

		if err := e.core.Push(t.Context(), plan); err != nil {
			t.Fatalf("Push: %v", err)
		}
		if diff := cmp.Diff([]string{objectKey("same")}, puts()); diff != "" {
			t.Errorf("PUT した object (-want +got):\n%s", diff)
		}
	})

	t.Run("5章/Push/manifest を書き、HEAD を新しい世代に向ける", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, files)

		if err := e.core.Push(t.Context(), plan); err != nil {
			t.Fatalf("Push: %v", err)
		}
		if head, _ := e.getObject(t, headKey()); head != pushGeneration {
			t.Errorf("HEAD = %q, want %q", head, pushGeneration)
		}
		body, ok := e.getObject(t, manifestKey(pushGeneration))
		if !ok {
			t.Fatal("manifest がない")
		}
		var m syncore.Manifest
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("manifest を読めない: %v", err)
		}
		if diff := cmp.Diff(entriesOf(files), m.Entries); diff != "" {
			t.Errorf("manifest の entries (-want +got):\n%s", diff)
		}
		if m.Version != 1 {
			t.Errorf("version = %d, want 1", m.Version)
		}
	})

	t.Run("5章/Push/manifest に createdAt と deviceName を書く", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, files)

		if err := e.core.Push(t.Context(), plan); err != nil {
			t.Fatalf("Push: %v", err)
		}
		body, _ := e.getObject(t, manifestKey(pushGeneration))
		var m syncore.Manifest
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("manifest を読めない: %v", err)
		}
		if m.CreatedAt != "2026-11-03T01:02:03.004Z" || m.DeviceName != testDeviceName {
			t.Errorf("(createdAt, deviceName) = (%q, %q), want (%q, %q)", m.CreatedAt, m.DeviceName, "2026-11-03T01:02:03.004Z", testDeviceName)
		}
	})

	t.Run("5章/Push/base を local の manifest ハッシュにする", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, files)

		if err := e.core.Push(t.Context(), plan); err != nil {
			t.Fatalf("Push: %v", err)
		}
		if got, want := e.base(t), syncore.ManifestHash(entriesOf(files)); got != want {
			t.Errorf("base = %q, want %q", got, want)
		}
	})

	t.Run("5章/Push/判定が Push でない plan はエラー", func(t *testing.T) {
		e := newEnv(t)
		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if plan.Decision != syncore.DecisionNoChange {
			t.Fatalf("Decision = %v, want 変更なし", plan.Decision)
		}

		err = e.core.Push(t.Context(), plan)
		if err == nil || errors.Is(err, syncore.ErrPlanStale) {
			t.Fatalf("Push error = %v, want an error other than ErrPlanStale", err)
		}
		if keys := e.keys(t, "games/"); len(keys) != 0 {
			t.Errorf("リモートに書いた: %v", keys)
		}
	})
}

func TestPush中止(t *testing.T) {
	files := map[string]string{"slot1.sav": "one", "slot2.sav": "two"}
	injected := errors.New("injected")

	t.Run("5章/Push/承認後にファイルが書き換わっていたら ErrPlanStale、HEAD を変えない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, files)
		writeFiles(t, e.saveDir, map[string]string{"slot2.sav": "changed"})

		if err := e.core.Push(t.Context(), plan); !errors.Is(err, syncore.ErrPlanStale) {
			t.Fatalf("Push error = %v, want ErrPlanStale", err)
		}
		if _, ok := e.getObject(t, headKey()); ok {
			t.Error("HEAD を書いた")
		}
		if base := e.base(t); base != "" {
			t.Errorf("base = %q, want 空", base)
		}
	})

	t.Run("5章/Push/承認後に HEAD が変わっていたら ErrPlanStale、HEAD を変えない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, files)
		e.putGeneration(t, entriesOf(map[string]string{"slot1.sav": "other"}))

		if err := e.core.Push(t.Context(), plan); !errors.Is(err, syncore.ErrPlanStale) {
			t.Fatalf("Push error = %v, want ErrPlanStale", err)
		}
		if head, _ := e.getObject(t, headKey()); head != testGeneration {
			t.Errorf("HEAD = %q, want %q", head, testGeneration)
		}
		if base := e.base(t); base != "" {
			t.Errorf("base = %q, want 空", base)
		}
	})

	t.Run("5章/Push/object の PUT が失敗したら中止、HEAD を変えない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, files)
		failPut(e, "/objects/", injected)

		if err := e.core.Push(t.Context(), plan); !errors.Is(err, injected) {
			t.Fatalf("Push error = %v, want %v", err, injected)
		}
		if keys := e.keys(t, "games/"+testGameID+"/manifests/"); len(keys) != 0 {
			t.Errorf("manifest を書いた: %v", keys)
		}
		if _, ok := e.getObject(t, headKey()); ok {
			t.Error("HEAD を書いた")
		}
	})

	t.Run("5章/Push/manifest の PUT が失敗したら中止、HEAD を変えない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, files)
		failPut(e, "/manifests/", injected)

		if err := e.core.Push(t.Context(), plan); !errors.Is(err, injected) {
			t.Fatalf("Push error = %v, want %v", err, injected)
		}
		if _, ok := e.getObject(t, headKey()); ok {
			t.Error("HEAD を書いた")
		}
		if base := e.base(t); base != "" {
			t.Errorf("base = %q, want 空", base)
		}
	})

	t.Run("5章/Push/HEAD の PUT が失敗したら base を書かない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, files)
		failPut(e, "/HEAD", injected)

		if err := e.core.Push(t.Context(), plan); !errors.Is(err, injected) {
			t.Fatalf("Push error = %v, want %v", err, injected)
		}
		if base := e.base(t); base != "" {
			t.Errorf("base = %q, want 空", base)
		}
	})
}

// 中断は、中断した直後のリモートの状態を直接作り、次の判定を確かめる。
func TestPush中断(t *testing.T) {
	files := map[string]string{"slot1.sav": "one"}

	t.Run("5章/中断/3〜5 で中断 → HEAD は同期前のまま、次の判定は Push", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		e.putObject(t, objectKey("one"), "one")
		e.putManifest(t, pushGeneration, entriesOf(files))

		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if plan.Decision != syncore.DecisionPush || plan.Remote.Hash != "" {
			t.Errorf("(Decision, Remote.Hash) = (%v, %q), want (Push, \"\")", plan.Decision, plan.Remote.Hash)
		}
	})

	t.Run("5章/中断/6 と 7 の間で中断 → 次の判定は 変更なし（base を記録）", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		e.putObject(t, objectKey("one"), "one")
		e.putGeneration(t, entriesOf(files))

		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if plan.Decision != syncore.DecisionNoChange || !plan.RecordBase {
			t.Errorf("(Decision, RecordBase) = (%v, %v), want (変更なし, true)", plan.Decision, plan.RecordBase)
		}
	})
}

func TestPush複数PC(t *testing.T) {
	t.Run("5章/複数 PC/Push したセーブを、別の PC の Status が Pull と判定する", func(t *testing.T) {
		files := map[string]string{"slot1.sav": "one"}
		a := newEnv(t)
		b := newEnvWithRemote(t, a.remote)
		plan := approvedPush(t, a, files)
		if err := a.core.Push(t.Context(), plan); err != nil {
			t.Fatalf("Push: %v", err)
		}

		got, err := b.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if got.Decision != syncore.DecisionPull || got.Remote.Hash != plan.Local.Hash {
			t.Errorf("(Decision, Remote.Hash) = (%v, %q), want (Pull, %q)", got.Decision, got.Remote.Hash, plan.Local.Hash)
		}
	})
}

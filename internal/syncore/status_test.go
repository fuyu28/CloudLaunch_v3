package syncore_test

import (
	"errors"
	"os"
	"testing"

	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

func TestStatusBaseなし(t *testing.T) {
	save := map[string]string{"slot1.sav": "local"}
	other := map[string]string{"slot1.sav": "remote"}

	tests := []struct {
		name           string
		local          map[string]string
		remote         map[string]string // nil なら HEAD を置かない
		wantDecision   syncore.Decision
		wantRecordBase bool
	}{
		{name: "4-D1 base なし・local なし・remote なし → 変更なし", wantDecision: syncore.DecisionNoChange},
		{name: "4-D2 base なし・local あり・remote なし → Push", local: save, wantDecision: syncore.DecisionPush},
		{name: "4-D3 base なし・local なし・remote あり → Pull", remote: other, wantDecision: syncore.DecisionPull},
		{name: "4-D4 base なし・local あり・remote あり（local と同じ） → 変更なし（base を記録）", local: save, remote: save, wantDecision: syncore.DecisionNoChange, wantRecordBase: true},
		{name: "4-D5 base なし・local あり・remote あり（local と異なる） → 競合", local: save, remote: other, wantDecision: syncore.DecisionConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			writeFiles(t, e.saveDir, tt.local)
			if tt.remote != nil {
				e.putGeneration(t, entriesOf(tt.remote))
			}

			plan, err := e.status(t)
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if plan.Decision != tt.wantDecision || plan.RecordBase != tt.wantRecordBase {
				t.Errorf("(Decision, RecordBase) = (%v, %v), want (%v, %v)", plan.Decision, plan.RecordBase, tt.wantDecision, tt.wantRecordBase)
			}
		})
	}
}

func TestStatusなしの意味(t *testing.T) {
	t.Run("4章/セーブフォルダがない → local なし", func(t *testing.T) {
		e := newEnv(t)
		if err := os.Remove(e.saveDir); err != nil {
			t.Fatalf("remove save dir: %v", err)
		}
		e.putGeneration(t, entriesOf(map[string]string{"slot1.sav": "remote"}))

		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if plan.Decision != syncore.DecisionPull || plan.Local.Hash != "" {
			t.Errorf("(Decision, Local.Hash) = (%v, %q), want (%v, \"\")", plan.Decision, plan.Local.Hash, syncore.DecisionPull)
		}
	})

	t.Run("4章/HEAD が空 manifest の世代を指す → remote あり", func(t *testing.T) {
		e := newEnv(t)
		e.putGeneration(t, []syncore.Entry{})

		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if want := syncore.ManifestHash(nil); plan.Decision != syncore.DecisionPull || plan.Remote.Hash != want {
			t.Errorf("(Decision, Remote.Hash) = (%v, %q), want (%v, %q)", plan.Decision, plan.Remote.Hash, syncore.DecisionPull, want)
		}
	})
}

func TestStatusBaseあり(t *testing.T) {
	t.Run("4章/base あり → 未実装のエラー", func(t *testing.T) {
		e := newEnv(t)
		e.setBase(t, syncore.ManifestHash(nil))

		if _, err := e.status(t); err == nil {
			t.Fatal("Status succeeded, want not implemented error")
		}
	})
}

func TestStatusリモートの失敗(t *testing.T) {
	t.Run("リモートの Get が失敗したらエラー", func(t *testing.T) {
		e := newEnv(t)
		injected := errors.New("injected")
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpGet {
				return injected
			}
			return nil
		}

		if _, err := e.status(t); !errors.Is(err, injected) {
			t.Fatalf("Status error = %v, want %v", err, injected)
		}
	})
}

// approvedRecordBase は、リモートと同じ files をセーブフォルダに置いて Status を呼び、判定が「base を記録」の plan を返す。
func approvedRecordBase(t *testing.T, e env, files map[string]string) syncore.Plan {
	t.Helper()
	writeFiles(t, e.saveDir, files)
	e.putGeneration(t, entriesOf(files))
	plan, err := e.status(t)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if plan.Decision != syncore.DecisionNoChange || !plan.RecordBase {
		t.Fatalf("(Decision, RecordBase) = (%v, %v), want (変更なし, true)", plan.Decision, plan.RecordBase)
	}
	return plan
}

func TestRecordBase(t *testing.T) {
	files := map[string]string{"slot1.sav": "one"}

	t.Run("4章/base を記録/base を local の manifest ハッシュにする", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedRecordBase(t, e, files)

		if err := e.core.RecordBase(t.Context(), plan); err != nil {
			t.Fatalf("RecordBase: %v", err)
		}
		if got, want := e.base(t), syncore.ManifestHash(entriesOf(files)); got != want {
			t.Errorf("base = %q, want %q", got, want)
		}
	})

	t.Run("4章/base を記録/リモートを読み書きしない", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedRecordBase(t, e, files)
		e.remote.Before = func(op memstore.Op, key string) error {
			t.Errorf("リモートを呼んだ: op=%v key=%s", op, key)
			return nil
		}

		if err := e.core.RecordBase(t.Context(), plan); err != nil {
			t.Fatalf("RecordBase: %v", err)
		}
	})

	t.Run("4章/base を記録/判定が「base を記録」でない plan はエラー", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, files)
		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if plan.Decision != syncore.DecisionPush {
			t.Fatalf("Decision = %v, want Push", plan.Decision)
		}

		if err := e.core.RecordBase(t.Context(), plan); err == nil {
			t.Fatal("RecordBase succeeded, want an error")
		}
		if base := e.base(t); base != "" {
			t.Errorf("base = %q, want 空", base)
		}
	})
}

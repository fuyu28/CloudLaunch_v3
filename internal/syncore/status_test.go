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
		{name: "4章/base なし・local なし・remote なし → 変更なし", wantDecision: syncore.DecisionNoChange},
		{name: "4章/base なし・local あり・remote なし → Push", local: save, wantDecision: syncore.DecisionPush},
		{name: "4章/base なし・local なし・remote あり → Pull", remote: other, wantDecision: syncore.DecisionPull},
		{name: "4章/base なし・local あり・remote あり（local と同じ） → 変更なし（base を記録）", local: save, remote: save, wantDecision: syncore.DecisionNoChange, wantRecordBase: true},
		{name: "4章/base なし・local あり・remote あり（local と異なる） → 競合", local: save, remote: other, wantDecision: syncore.DecisionConflict},
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

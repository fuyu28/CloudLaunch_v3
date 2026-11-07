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
	base := map[string]string{"slot1.sav": "base"}
	changed := map[string]string{"slot1.sav": "changed"}
	other := map[string]string{"slot1.sav": "other"}

	tests := []struct {
		name             string
		local            map[string]string // nil ならセーブフォルダにファイルを置かない
		remote           map[string]string // nil なら HEAD を置かない
		wantDecision     syncore.Decision
		wantRecordBase   bool
		wantLocalMissing bool
	}{
		{name: "4-D6 base B・local B・remote B → 変更なし", local: base, remote: base, wantDecision: syncore.DecisionNoChange},
		{name: "4-D7 base B・local 変化・remote B → Push", local: changed, remote: base, wantDecision: syncore.DecisionPush},
		{name: "4-D8 base B・local B・remote 変化 → Pull", local: base, remote: other, wantDecision: syncore.DecisionPull},
		{name: "4-D9 base B・local 変化・remote 変化（local と同じ） → 変更なし（base を更新）", local: changed, remote: changed, wantDecision: syncore.DecisionNoChange, wantRecordBase: true},
		{name: "4-D10 base B・local 変化・remote 変化（local と異なる） → 競合", local: changed, remote: other, wantDecision: syncore.DecisionConflict},
		{name: "4-D11 base B・local なし・remote B または変化 → 競合（ローカルにセーブが見つからない）", remote: other, wantDecision: syncore.DecisionConflict, wantLocalMissing: true},
		{name: "4-D12 base B・local B または変化・remote なし → Push", local: changed, wantDecision: syncore.DecisionPush},
		{name: "4-D13 base B・local なし・remote なし → 変更なし", wantDecision: syncore.DecisionNoChange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.setBase(t, syncore.ManifestHash(entriesOf(base)))
			writeFiles(t, e.saveDir, tt.local)
			if tt.remote != nil {
				e.putGeneration(t, entriesOf(tt.remote))
			}

			plan, err := e.status(t)
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			got := [3]any{plan.Decision, plan.RecordBase, plan.LocalMissing}
			want := [3]any{tt.wantDecision, tt.wantRecordBase, tt.wantLocalMissing}
			if got != want {
				t.Errorf("(Decision, RecordBase, LocalMissing) = %v, want %v", got, want)
			}
		})
	}

	t.Run("4章/base が空 manifest なら、ファイルのない local を空 manifest として比べる", func(t *testing.T) {
		e := newEnv(t)
		empty := syncore.ManifestHash(nil)
		e.setBase(t, empty)
		e.putGeneration(t, []syncore.Entry{})

		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if plan.Decision != syncore.DecisionNoChange || plan.LocalMissing || plan.Local.Hash != empty {
			t.Errorf("(Decision, LocalMissing, Local.Hash) = (%v, %v, %q), want (変更なし, false, %q)", plan.Decision, plan.LocalMissing, plan.Local.Hash, empty)
		}
	})

	t.Run("4章/base を更新/base を local の manifest ハッシュにする", func(t *testing.T) {
		e := newEnv(t)
		e.setBase(t, syncore.ManifestHash(entriesOf(base)))
		writeFiles(t, e.saveDir, changed)
		e.putGeneration(t, entriesOf(changed))
		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if !plan.RecordBase {
			t.Fatalf("RecordBase = false, want true")
		}

		if err := e.core.RecordBase(t.Context(), plan); err != nil {
			t.Fatalf("RecordBase: %v", err)
		}
		if got, want := e.base(t), syncore.ManifestHash(entriesOf(changed)); got != want {
			t.Errorf("base = %q, want %q", got, want)
		}
	})
}

func TestStatus複数PC(t *testing.T) {
	t.Run("4章/複数 PC/Push と Pull の後に、相手の Push を Pull と判定する", func(t *testing.T) {
		a := newEnv(t)
		b := newEnvWithRemote(t, a.remote)
		removeSaveDir(t, b)
		if err := a.core.Push(t.Context(), approvedPush(t, a, map[string]string{"slot1.sav": "one"}), syncore.PushOptions{}); err != nil {
			t.Fatalf("A の Push: %v", err)
		}
		pull, err := b.status(t)
		if err != nil {
			t.Fatalf("B の Status: %v", err)
		}
		if err := b.core.Pull(t.Context(), pull, syncore.PullOptions{}); err != nil {
			t.Fatalf("B の Pull: %v", err)
		}

		writeFiles(t, a.saveDir, map[string]string{"slot1.sav": "two"})
		push, err := a.status(t)
		if err != nil {
			t.Fatalf("A の 2 回目の Status: %v", err)
		}
		if err := a.core.Push(t.Context(), push, syncore.PushOptions{}); err != nil {
			t.Fatalf("A の 2 回目の Push: %v", err)
		}

		got, err := b.status(t)
		if err != nil {
			t.Fatalf("B の 2 回目の Status: %v", err)
		}
		if got.Decision != syncore.DecisionPull {
			t.Errorf("Decision = %v, want Pull", got.Decision)
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
		plan := e.planWant(t, syncore.DecisionPush)

		if err := e.core.RecordBase(t.Context(), plan); err == nil {
			t.Fatal("RecordBase succeeded, want an error")
		}
		if base := e.base(t); base != "" {
			t.Errorf("base = %q, want 空", base)
		}
	})
}

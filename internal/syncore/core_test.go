package syncore_test

import (
	"testing"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

func TestNew(t *testing.T) {
	t.Run("コアの設定/Logger を省いても、ログを捨てて Push を終える", func(t *testing.T) {
		e := newEnv(t)
		plan := approvedPush(t, e, map[string]string{"slot.sav": "one"})
		core := e.coreWithout(func(cfg *syncore.Config) { cfg.Logger = nil })

		// Push は HEAD と base を書いた後にログを出すので、Logger が nil のままだとそこで panic する
		if err := core.Push(t.Context(), plan, syncore.PushOptions{}); err != nil {
			t.Fatalf("Push: %v", err)
		}
		if head, _ := e.getObject(t, headKey()); head != pushGeneration {
			t.Errorf("HEAD = %q, want %q", head, pushGeneration)
		}
	})
}

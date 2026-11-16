package metasync_test

import (
	"testing"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
)

func TestNew(t *testing.T) {
	t.Run("8章/ゲーム情報/Logger を省いても、ログを捨てて不正な game.json を飛ばす", func(t *testing.T) {
		e := newEnvOn(t, memstore.New())
		e.putRemote(gameKey, `{"id":`)
		core := metasync.New(metasync.Config{Remote: e.remote, Local: e.store, ImageDir: e.imageDir})

		// 不正な game.json を飛ばすときに Warn を出すので、Logger が nil のままだとそこで panic する
		got, err := core.Sync(t.Context(), testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if got.GameInfo != metasync.GameInfoInvalid {
			t.Errorf("GameInfo = %v, want GameInfoInvalid", got.GameInfo)
		}
	})
}

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

func TestRemoteDeleted(t *testing.T) {
	ctx := context.Background()

	t.Run("store/RemoteDeleted はゲームの tombstone の記録がなければ false を返す", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)

		_, ok, err := s.RemoteDeleted(ctx, testGameID)
		if err != nil {
			t.Fatalf("RemoteDeleted: %v", err)
		}
		if ok {
			t.Error("RemoteDeleted = true, want false")
		}
	})

	t.Run("store/RecordRemoteDeleted で書いた記録を RemoteDeleted が UTC のミリ秒で返す", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		jst := time.FixedZone("JST", 9*60*60)
		if err := s.RecordRemoteDeleted(ctx, testGameID, syncore.Tombstone{
			DeletedAt: time.Date(2026, 11, 3, 9, 0, 0, 1_234_567, jst), DeviceName: "Laptop",
		}); err != nil {
			t.Fatalf("RecordRemoteDeleted: %v", err)
		}

		got, ok, err := s.RemoteDeleted(ctx, testGameID)
		if err != nil {
			t.Fatalf("RemoteDeleted: %v", err)
		}
		if !ok {
			t.Fatal("RemoteDeleted = false, want true")
		}
		want := syncore.Tombstone{DeletedAt: time.Date(2026, 11, 3, 0, 0, 0, 1_000_000, time.UTC), DeviceName: "Laptop"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("RemoteDeleted (-want +got):\n%s", diff)
		}
	})

	t.Run("store/IsRemoteDeleted はゲームの tombstone の記録の有無を返す", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)

		if deleted, err := s.IsRemoteDeleted(ctx, testGameID); err != nil || deleted {
			t.Fatalf("記録の前: IsRemoteDeleted = %v, %v, want false, nil", deleted, err)
		}
		if err := s.RecordRemoteDeleted(ctx, testGameID, syncore.Tombstone{DeletedAt: time.Now(), DeviceName: "Laptop"}); err != nil {
			t.Fatalf("RecordRemoteDeleted: %v", err)
		}
		if deleted, err := s.IsRemoteDeleted(ctx, testGameID); err != nil || !deleted {
			t.Errorf("記録の後: IsRemoteDeleted = %v, %v, want true, nil", deleted, err)
		}
	})

	t.Run("store/RemoteDeleted は登録していないゲームでエラーを返す", func(t *testing.T) {
		s, _ := openTemp(t)

		if _, _, err := s.RemoteDeleted(ctx, testGameID); err == nil {
			t.Error("RemoteDeleted がエラーを返さなかった")
		}
	})

	t.Run("store/RecordRemoteDeleted は登録していないゲームでエラーを返す", func(t *testing.T) {
		s, _ := openTemp(t)

		if err := s.RecordRemoteDeleted(ctx, testGameID, syncore.Tombstone{DeletedAt: time.Now(), DeviceName: "Laptop"}); err == nil {
			t.Error("RecordRemoteDeleted がエラーを返さなかった")
		}
	})
}

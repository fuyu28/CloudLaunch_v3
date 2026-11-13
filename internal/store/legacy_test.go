package store

import (
	"context"
	"testing"
	"time"

	"github.com/fuyu28/CloudLaunch_v3/internal/legacy"
)

func TestAddLegacySessions(t *testing.T) {
	valid := legacy.Session{
		ID: "0123456789abcdef0123456789abcd01", GameID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", GameTitle: "ゲーム",
		PlayedAt: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), DurationSec: 1,
	}
	invalidID := valid
	invalidID.ID = "0192d4e8-0000-7000-8000-0000000000c1"
	invalidTime := valid
	invalidTime.ID = "0123456789abcdef0123456789abcd02"
	invalidTime.PlayedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, tt := range []struct {
		name    string
		invalid legacy.Session
	}{
		{"32 桁の小文字 16 進でない ID", invalidID},
		{"UTC の年が 4 桁に収まらない日時", invalidTime},
	} {
		t.Run("store/AddLegacySessions は"+tt.name+"があれば何も入れない", func(t *testing.T) {
			s, _ := openTemp(t)
			ctx := context.Background()

			if _, err := s.AddLegacySessions(ctx, []legacy.Session{valid, tt.invalid}); err == nil {
				t.Errorf("AddLegacySessions がエラーを返さなかった")
			}
			got, err := s.LegacySessions(ctx)
			if err != nil {
				t.Fatalf("LegacySessions: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("LegacySessions = %v, want 空", got)
			}
		})
	}
}

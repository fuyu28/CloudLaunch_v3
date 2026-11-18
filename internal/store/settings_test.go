package store

import (
	"context"
	"testing"
)

func TestSettings(t *testing.T) {
	ctx := context.Background()

	t.Run("3章/設定/保存していない key は、ないことが分かる", func(t *testing.T) {
		s, _ := openTemp(t)

		v, ok, err := s.Setting(ctx, "deviceName")
		if err != nil {
			t.Fatalf("Setting: %v", err)
		}
		if ok || v != "" {
			t.Errorf("Setting = (%q, %v), want (\"\", false)", v, ok)
		}
	})

	t.Run("3章/設定/保存した値を読み戻せ、もう一度保存すると上書きする", func(t *testing.T) {
		s, _ := openTemp(t)
		if err := s.SetSettings(ctx, map[string]string{"a": "1", "b": "2"}); err != nil {
			t.Fatalf("SetSettings: %v", err)
		}
		if err := s.SetSettings(ctx, map[string]string{"a": "3"}); err != nil {
			t.Fatalf("SetSettings: %v", err)
		}

		for key, want := range map[string]string{"a": "3", "b": "2"} {
			v, ok, err := s.Setting(ctx, key)
			if err != nil {
				t.Fatalf("Setting(%q): %v", key, err)
			}
			if !ok || v != want {
				t.Errorf("Setting(%q) = (%q, %v), want (%q, true)", key, v, ok, want)
			}
		}
	})
}

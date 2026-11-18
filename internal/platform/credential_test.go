package platform

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/settings"
)

// newTestCredentialStore は、Windows では本番の CloudLaunch/R2 を壊さないよう、テストごとのターゲット名を使い、終わったら消す。
func newTestCredentialStore(t *testing.T) *CredentialStore {
	t.Helper()
	c := NewCredentialStore(fmt.Sprintf("CloudLaunchTest/%d", time.Now().UnixNano()), t.TempDir())
	t.Cleanup(func() { _ = c.delete() })
	return c
}

func TestCredentialStore(t *testing.T) {
	t.Run("3章/資格情報/保存していなければ、ないことが分かる", func(t *testing.T) {
		c := newTestCredentialStore(t)

		got, ok, err := c.Credential()
		if err != nil {
			t.Fatalf("Credential: %v", err)
		}
		if ok || got != (settings.Credential{}) {
			t.Errorf("Credential = (%+v, %v), want zero, false", got, ok)
		}
	})

	t.Run("3章/資格情報/保存して読み戻せ、もう一度保存すると上書きする", func(t *testing.T) {
		c := newTestCredentialStore(t)
		if err := c.SetCredential(settings.Credential{AccessKeyID: "old-key", SecretAccessKey: "old-secret"}); err != nil {
			t.Fatalf("SetCredential: %v", err)
		}
		want := settings.Credential{AccessKeyID: "key", SecretAccessKey: "秘密/secret"}
		if err := c.SetCredential(want); err != nil {
			t.Fatalf("SetCredential: %v", err)
		}

		got, ok, err := c.Credential()
		if err != nil {
			t.Fatalf("Credential: %v", err)
		}
		if !ok {
			t.Fatal("Credential: ok = false, want true")
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Credential (-want +got):\n%s", diff)
		}
	})
}

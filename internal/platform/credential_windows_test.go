package platform

import (
	"errors"
	"testing"

	"github.com/danieljoos/wincred"
)

// deleteTestCredential は、資格情報マネージャーに残ったテストの資格情報を消す。
func deleteTestCredential(t *testing.T, c *CredentialStore) {
	t.Helper()
	g, err := wincred.GetGenericCredential(c.target)
	if errors.Is(err, wincred.ErrElementNotFound) {
		return
	}
	if err != nil {
		t.Errorf("read credential %s: %v", c.target, err)
		return
	}
	if err := g.Delete(); err != nil {
		t.Errorf("delete credential %s: %v", c.target, err)
	}
}

// Windows 資格情報マネージャーへのアクセスキーの読み書き。
package platform

import (
	"errors"
	"fmt"

	"github.com/danieljoos/wincred"

	"github.com/fuyu28/CloudLaunch_v3/internal/settings"
)

// Credential は、汎用の資格情報の UserName をアクセスキー ID、CredentialBlob を UTF-8 のシークレットとして読む。
func (c *CredentialStore) Credential() (settings.Credential, bool, error) {
	g, err := wincred.GetGenericCredential(c.target)
	if errors.Is(err, wincred.ErrElementNotFound) {
		return settings.Credential{}, false, nil
	}
	if err != nil {
		return settings.Credential{}, false, fmt.Errorf("read credential %s: %w", c.target, err)
	}
	return settings.Credential{AccessKeyID: g.UserName, SecretAccessKey: string(g.CredentialBlob)}, true, nil
}

func (c *CredentialStore) SetCredential(cred settings.Credential) error {
	g := wincred.NewGenericCredential(c.target)
	g.UserName = cred.AccessKeyID
	g.CredentialBlob = []byte(cred.SecretAccessKey)
	if err := g.Write(); err != nil {
		return fmt.Errorf("write credential %s: %w", c.target, err)
	}
	return nil
}

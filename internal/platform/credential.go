// R2 のアクセスキーの置き場所（仕様書 3 章）。置き場所そのものは OS ごとのファイルにある。
package platform

import "github.com/fuyu28/CloudLaunch_v3/internal/settings"

type CredentialStore struct {
	target string
	dir    string
}

func NewCredentialStore(target, dir string) *CredentialStore {
	return &CredentialStore{target: target, dir: dir}
}

func (c *CredentialStore) Credential() (settings.Credential, bool, error) {
	return settings.Credential{}, false, nil
}

func (c *CredentialStore) SetCredential(cred settings.Credential) error {
	return nil
}

func (c *CredentialStore) delete() error {
	return nil
}

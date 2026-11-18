// Linux（開発用）のアクセスキーの置き場所。Secret Service を使わず、0600 のファイルに平文で置く。
package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fuyu28/CloudLaunch_v3/internal/settings"
)

type credentialFile struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
}

func (c *CredentialStore) path() string {
	return filepath.Join(c.dir, "credential.json")
}

func (c *CredentialStore) Credential() (settings.Credential, bool, error) {
	b, err := os.ReadFile(c.path())
	if errors.Is(err, fs.ErrNotExist) {
		return settings.Credential{}, false, nil
	}
	if err != nil {
		return settings.Credential{}, false, fmt.Errorf("read credential: %w", err)
	}
	var f credentialFile
	if err := json.Unmarshal(b, &f); err != nil {
		return settings.Credential{}, false, fmt.Errorf("parse credential %s: %w", c.path(), err)
	}
	return settings.Credential{AccessKeyID: f.AccessKeyID, SecretAccessKey: f.SecretAccessKey}, true, nil
}

// SetCredential は、一時ファイルに書いて fsync してからリネームし、書き込みの途中で落ちても、前の資格情報か新しい資格情報の
// どちらかを残す。fsync しないと、電源が落ちたときに長さ 0 のファイルが残りうる。
func (c *CredentialStore) SetCredential(cred settings.Credential) error {
	b, err := json.Marshal(credentialFile(cred))
	if err != nil {
		return fmt.Errorf("encode credential: %w", err)
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return fmt.Errorf("create credential dir: %w", err)
	}
	tmp, err := os.CreateTemp(c.dir, "credential-*.json")
	if err != nil {
		return fmt.Errorf("create credential: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write credential: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync credential: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write credential: %w", err)
	}
	if err := os.Rename(tmp.Name(), c.path()); err != nil {
		return fmt.Errorf("write credential: %w", err)
	}
	return nil
}

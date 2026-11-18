// R2 の接続情報と deviceName を、環境変数・DB の設定・資格情報から組み立て、保存する（仕様書 3 章）。CLI と GUI が同じ順番で
// 読むよう、ここに 1 つだけ置く。
package settings

import (
	"context"
	"errors"
)

var (
	// ErrRemoteNotConfigured は、接続情報が揃っていないことを表す。つなぐ前に見分けて、設定を入れるよう案内するために使う
	ErrRemoteNotConfigured = errors.New("remote is not configured")
	// ErrInvalidSettings は、保存しようとした値が検証を通らなかったことを表す。何も保存していない
	ErrInvalidSettings = errors.New("invalid settings")
)

// Local は DB の設定。
type Local interface {
	Setting(ctx context.Context, key string) (string, bool, error)
	// SetSettings は、values のすべてを書くか、何も書かないかのどちらかにする。
	SetSettings(ctx context.Context, values map[string]string) error
}

type Credential struct {
	AccessKeyID     string
	SecretAccessKey string
}

// Credentials は、OS の資格情報の置き場所。
type Credentials interface {
	Credential() (Credential, bool, error)
	SetCredential(c Credential) error
}

type Config struct {
	Local       Local
	Credentials Credentials
	// Getenv は、nil なら os.Getenv を使う
	Getenv func(key string) string
	// Hostname は、nil なら os.Hostname を使う
	Hostname func() (string, error)
}

type Settings struct {
	cfg Config
}

func New(cfg Config) *Settings {
	return &Settings{cfg: cfg}
}

type Remote struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
}

// RemoteInput は、設定画面で入れた接続情報。SecretAccessKey が空なら、保存してある値を残す。
type RemoteInput struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
}

func (s *Settings) Remote(ctx context.Context) (Remote, error) {
	return Remote{}, nil
}

func (s *Settings) UsesEnv() bool {
	return false
}

func (s *Settings) SaveRemote(ctx context.Context, in RemoteInput) error {
	return nil
}

func (s *Settings) DeviceName(ctx context.Context) (string, error) {
	return "", nil
}

func (s *Settings) SaveDeviceName(ctx context.Context, name string) error {
	return nil
}

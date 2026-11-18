// R2 の接続情報と deviceName を、環境変数・DB の設定・資格情報から組み立て、保存する（仕様書 3 章）。CLI と GUI が同じ順番で
// 読むよう、ここに 1 つだけ置く。
package settings

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"
)

var (
	// ErrRemoteNotConfigured は、接続情報が揃っていないことを表す。つなぐ前に見分けて、設定を入れるよう案内するために使う
	ErrRemoteNotConfigured = errors.New("remote is not configured")
	// ErrInvalidSettings は、保存しようとした値が検証を通らなかったことを表す。何も保存していない
	ErrInvalidSettings = errors.New("invalid settings")
)

const (
	keyEndpoint   = "remote.endpoint"
	keyRegion     = "remote.region"
	keyBucket     = "remote.bucket"
	keyDeviceName = "deviceName"

	envEndpoint        = "CLOUDLAUNCH_S3_ENDPOINT"
	envRegion          = "CLOUDLAUNCH_S3_REGION"
	envBucket          = "CLOUDLAUNCH_S3_BUCKET"
	envAccessKeyID     = "CLOUDLAUNCH_S3_ACCESS_KEY_ID"
	envSecretAccessKey = "CLOUDLAUNCH_S3_SECRET_ACCESS_KEY"

	defaultRegion = "auto"
	// maxDeviceNameLen は rune の数。deviceName は確認ダイアログと tombstone の表示に出るだけなので、画面に収まる長さで止める
	maxDeviceNameLen = 64
)

type Local interface {
	Setting(ctx context.Context, key string) (string, bool, error)
	// SetSettings は、values のすべてを書くか、何も書かないかのどちらかにする。
	SetSettings(ctx context.Context, values map[string]string) error
}

type Credential struct {
	AccessKeyID     string
	SecretAccessKey string
}

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
	if cfg.Getenv == nil {
		cfg.Getenv = os.Getenv
	}
	if cfg.Hostname == nil {
		cfg.Hostname = os.Hostname
	}
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

// Remote は、CLOUDLAUNCH_S3_* のどれか 1 つでもあれば、すべてを環境変数から読み、保存した値と混ぜない。どこから読んだかに
// 気づきにくい組み合わせを作らないため。Region だけは、空なら auto にする。
func (s *Settings) Remote(ctx context.Context) (Remote, error) {
	var r Remote
	if s.UsesEnv() {
		r = Remote{
			Endpoint:        s.cfg.Getenv(envEndpoint),
			Region:          s.cfg.Getenv(envRegion),
			Bucket:          s.cfg.Getenv(envBucket),
			AccessKeyID:     s.cfg.Getenv(envAccessKeyID),
			SecretAccessKey: s.cfg.Getenv(envSecretAccessKey),
		}
	} else {
		var err error
		if r, err = s.savedRemote(ctx); err != nil {
			return Remote{}, err
		}
	}
	if r.Endpoint == "" || r.Bucket == "" || r.AccessKeyID == "" || r.SecretAccessKey == "" {
		return Remote{}, ErrRemoteNotConfigured
	}
	if r.Region == "" {
		r.Region = defaultRegion
	}
	return r, nil
}

func (s *Settings) savedRemote(ctx context.Context) (Remote, error) {
	var r Remote
	for key, dst := range map[string]*string{keyEndpoint: &r.Endpoint, keyRegion: &r.Region, keyBucket: &r.Bucket} {
		v, _, err := s.cfg.Local.Setting(ctx, key)
		if err != nil {
			return Remote{}, fmt.Errorf("read setting %s: %w", key, err)
		}
		*dst = v
	}
	c, ok, err := s.cfg.Credentials.Credential()
	if err != nil {
		return Remote{}, fmt.Errorf("read credential: %w", err)
	}
	if ok {
		r.AccessKeyID, r.SecretAccessKey = c.AccessKeyID, c.SecretAccessKey
	}
	return r, nil
}

// SavedRemote は、設定画面に出す保存した接続情報。画面に出す必要のないシークレットは持たず、保存してあるかだけを持つ。
// Region は、空なら空のまま持つ。
type SavedRemote struct {
	Endpoint    string
	Region      string
	Bucket      string
	AccessKeyID string
	HasSecret   bool
}

// SavedRemote は、環境変数が効いていても、保存した値を返す。
func (s *Settings) SavedRemote(ctx context.Context) (SavedRemote, error) {
	return SavedRemote{}, nil
}

func (s *Settings) UsesEnv() bool {
	for _, k := range []string{envEndpoint, envRegion, envBucket, envAccessKeyID, envSecretAccessKey} {
		if s.cfg.Getenv(k) != "" {
			return true
		}
	}
	return false
}

// SaveRemote は、資格情報を書いてから DB の設定を書く。2 つは 1 つのトランザクションにできないので、DB の書き込みに失敗すると、
// 新しい資格情報と古い DB の設定が残る。そのときはエラーを返し、もう一度保存すれば揃う。
func (s *Settings) SaveRemote(ctx context.Context, in RemoteInput) error {
	endpoint := strings.TrimSpace(in.Endpoint)
	region := strings.TrimSpace(in.Region)
	bucket := strings.TrimSpace(in.Bucket)
	// 貼り付けた末尾の改行や空白をシークレットに含めると、保存は通っても R2 の署名が合わず、原因の分かりにくい認証エラーになる
	cred := Credential{AccessKeyID: strings.TrimSpace(in.AccessKeyID), SecretAccessKey: strings.TrimSpace(in.SecretAccessKey)}

	if u, err := url.Parse(endpoint); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: endpoint must be an http or https URL: %q", ErrInvalidSettings, endpoint)
	}
	if bucket == "" {
		return fmt.Errorf("%w: empty bucket", ErrInvalidSettings)
	}
	if cred.AccessKeyID == "" {
		return fmt.Errorf("%w: empty access key ID", ErrInvalidSettings)
	}
	if cred.SecretAccessKey == "" {
		saved, ok, err := s.cfg.Credentials.Credential()
		if err != nil {
			return fmt.Errorf("read credential: %w", err)
		}
		if !ok || saved.SecretAccessKey == "" {
			return fmt.Errorf("%w: empty secret access key", ErrInvalidSettings)
		}
		// キーを作り直すと ID とシークレットは対で変わるので、新しい ID に古いシークレットを組み合わせると認証エラーになる
		if saved.AccessKeyID != cred.AccessKeyID {
			return fmt.Errorf("%w: secret access key is required when the access key ID changes", ErrInvalidSettings)
		}
		cred.SecretAccessKey = saved.SecretAccessKey
	}

	if err := s.cfg.Credentials.SetCredential(cred); err != nil {
		return fmt.Errorf("write credential: %w", err)
	}
	if err := s.cfg.Local.SetSettings(ctx, map[string]string{keyEndpoint: endpoint, keyRegion: region, keyBucket: bucket}); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

// DeviceName は、保存していなければ、読むたびにホスト名を返す。初めから DB に書くと、ホスト名を変えた PC で古い名前が残るため。
// ホスト名にも保存するときと同じ検証をかけ、通らなければエラーを返す。
func (s *Settings) DeviceName(ctx context.Context) (string, error) {
	v, ok, err := s.cfg.Local.Setting(ctx, keyDeviceName)
	if err != nil {
		return "", fmt.Errorf("read setting %s: %w", keyDeviceName, err)
	}
	if ok {
		return v, nil
	}
	host, err := s.cfg.Hostname()
	if err != nil {
		return "", fmt.Errorf("hostname: %w", err)
	}
	host, err = checkDeviceName(host)
	if err != nil {
		return "", fmt.Errorf("hostname: %w", err)
	}
	return host, nil
}

func (s *Settings) SaveDeviceName(ctx context.Context, name string) error {
	name, err := checkDeviceName(name)
	if err != nil {
		return err
	}
	if err := s.cfg.Local.SetSettings(ctx, map[string]string{keyDeviceName: name}); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

// checkDeviceName は、前後の空白を除いた名前を返す。空の deviceName は、ゲームの tombstone で不正とする（仕様書 8-T5）ので拒否する。
func checkDeviceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("%w: empty device name", ErrInvalidSettings)
	}
	if n := utf8.RuneCountInString(name); n > maxDeviceNameLen {
		return "", fmt.Errorf("%w: device name is %d characters, longer than %d", ErrInvalidSettings, n, maxDeviceNameLen)
	}
	return name, nil
}

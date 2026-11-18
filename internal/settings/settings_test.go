package settings_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/settings"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
)

type memCredentials struct {
	cred settings.Credential
	ok   bool
}

func (m *memCredentials) Credential() (settings.Credential, bool, error) {
	return m.cred, m.ok, nil
}

func (m *memCredentials) SetCredential(c settings.Credential) error {
	m.cred, m.ok = c, true
	return nil
}

type fixture struct {
	settings *settings.Settings
	creds    *memCredentials
}

// newFixture は、env にない環境変数を空として返し、ホスト名を "host-pc" にする。
func newFixture(t *testing.T, env map[string]string) fixture {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "cloudlaunch.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	creds := &memCredentials{}
	return fixture{
		settings: settings.New(settings.Config{
			Local:       s,
			Credentials: creds,
			Getenv:      func(k string) string { return env[k] },
			Hostname:    func() (string, error) { return "host-pc", nil },
		}),
		creds: creds,
	}
}

var savedInput = settings.RemoteInput{
	Endpoint:        "https://account.r2.cloudflarestorage.com",
	Region:          "auto",
	Bucket:          "saved-bucket",
	AccessKeyID:     "saved-key",
	SecretAccessKey: "saved-secret",
}

var savedRemote = settings.Remote{
	Endpoint:        "https://account.r2.cloudflarestorage.com",
	Region:          "auto",
	Bucket:          "saved-bucket",
	AccessKeyID:     "saved-key",
	SecretAccessKey: "saved-secret",
}

var fullEnv = map[string]string{
	"CLOUDLAUNCH_S3_ENDPOINT":          "http://localhost:7070",
	"CLOUDLAUNCH_S3_REGION":            "us-east-1",
	"CLOUDLAUNCH_S3_BUCKET":            "env-bucket",
	"CLOUDLAUNCH_S3_ACCESS_KEY_ID":     "env-key",
	"CLOUDLAUNCH_S3_SECRET_ACCESS_KEY": "env-secret",
}

func save(t *testing.T, f fixture, in settings.RemoteInput) {
	t.Helper()
	if err := f.settings.SaveRemote(context.Background(), in); err != nil {
		t.Fatalf("SaveRemote: %v", err)
	}
}

func TestRemote(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		env  map[string]string
		// saved は、Remote を呼ぶ前に SaveRemote で保存する値。nil なら保存しない
		saved   *settings.RemoteInput
		want    settings.Remote
		wantEnv bool
	}{
		{
			name:  "3章/接続情報/環境変数がなければ、DB の設定と資格情報から読む",
			saved: &savedInput,
			want:  savedRemote,
		},
		{
			name:  "3章/接続情報/環境変数が 1 つでもあれば、すべて環境変数から読み、保存した値を使わない",
			env:   fullEnv,
			saved: &savedInput,
			want: settings.Remote{
				Endpoint: "http://localhost:7070", Region: "us-east-1", Bucket: "env-bucket",
				AccessKeyID: "env-key", SecretAccessKey: "env-secret",
			},
			wantEnv: true,
		},
		{
			name: "3章/接続情報/Region が空なら auto にする（環境変数）",
			env: map[string]string{
				"CLOUDLAUNCH_S3_ENDPOINT":          "http://localhost:7070",
				"CLOUDLAUNCH_S3_BUCKET":            "env-bucket",
				"CLOUDLAUNCH_S3_ACCESS_KEY_ID":     "env-key",
				"CLOUDLAUNCH_S3_SECRET_ACCESS_KEY": "env-secret",
			},
			want: settings.Remote{
				Endpoint: "http://localhost:7070", Region: "auto", Bucket: "env-bucket",
				AccessKeyID: "env-key", SecretAccessKey: "env-secret",
			},
			wantEnv: true,
		},
		{
			name: "3章/接続情報/Region が空なら auto にする（保存した値）",
			saved: &settings.RemoteInput{
				Endpoint: savedInput.Endpoint, Bucket: savedInput.Bucket,
				AccessKeyID: savedInput.AccessKeyID, SecretAccessKey: savedInput.SecretAccessKey,
			},
			want: savedRemote,
		},
		{
			name: "3章/接続情報/保存した値は前後の空白を除く",
			saved: &settings.RemoteInput{
				Endpoint: " " + savedInput.Endpoint + " ", Region: " auto ", Bucket: " saved-bucket ",
				AccessKeyID: " saved-key ", SecretAccessKey: savedInput.SecretAccessKey,
			},
			want: savedRemote,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, tt.env)
			if tt.saved != nil {
				save(t, f, *tt.saved)
			}

			got, err := f.settings.Remote(ctx)
			if err != nil {
				t.Fatalf("Remote: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Remote (-want +got):\n%s", diff)
			}
			if gotEnv := f.settings.UsesEnv(); gotEnv != tt.wantEnv {
				t.Errorf("UsesEnv = %v, want %v", gotEnv, tt.wantEnv)
			}
		})
	}
}

func TestRemoteNotConfigured(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		env  map[string]string
		// setup は、Remote を呼ぶ前の状態を作る
		setup func(t *testing.T, f fixture)
	}{
		{
			name:  "3章/接続情報/何も保存していなければ、ErrRemoteNotConfigured を返す",
			setup: func(*testing.T, fixture) {},
		},
		{
			name: "3章/接続情報/DB の設定か資格情報のどちらかがなければ、ErrRemoteNotConfigured を返す（資格情報がない）",
			setup: func(t *testing.T, f fixture) {
				save(t, f, savedInput)
				f.creds.ok = false
			},
		},
		{
			name: "3章/接続情報/DB の設定か資格情報のどちらかがなければ、ErrRemoteNotConfigured を返す（DB の設定がない）",
			setup: func(t *testing.T, f fixture) {
				if err := f.creds.SetCredential(settings.Credential{AccessKeyID: "k", SecretAccessKey: "s"}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "3章/接続情報/環境変数で足りない項目があれば、ErrRemoteNotConfigured を返す",
			env:  map[string]string{"CLOUDLAUNCH_S3_ENDPOINT": "http://localhost:7070"},
			setup: func(t *testing.T, f fixture) {
				save(t, f, savedInput)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, tt.env)
			tt.setup(t, f)

			_, err := f.settings.Remote(ctx)
			if !errors.Is(err, settings.ErrRemoteNotConfigured) {
				t.Errorf("Remote: err = %v, want ErrRemoteNotConfigured", err)
			}
		})
	}
}

func TestSaveRemote(t *testing.T) {
	ctx := context.Background()

	keep := []struct {
		name   string
		secret string
	}{
		{"3章/接続情報/保存するとき、シークレットが空なら今の値を残す", ""},
		{"3章/接続情報/保存するとき、シークレットが空白だけなら今の値を残す", " \n"},
	}
	for _, tt := range keep {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, nil)
			save(t, f, savedInput)
			in := savedInput
			in.Bucket, in.SecretAccessKey = "new-bucket", tt.secret
			save(t, f, in)

			got, err := f.settings.Remote(ctx)
			if err != nil {
				t.Fatalf("Remote: %v", err)
			}
			want := savedRemote
			want.Bucket = "new-bucket"
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Remote (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("3章/接続情報/シークレットの前後の空白を除いて保存する", func(t *testing.T) {
		f := newFixture(t, nil)
		in := savedInput
		in.SecretAccessKey = " saved-secret\n"
		save(t, f, in)

		got, err := f.settings.Remote(ctx)
		if err != nil {
			t.Fatalf("Remote: %v", err)
		}
		if diff := cmp.Diff(savedRemote, got); diff != "" {
			t.Errorf("Remote (-want +got):\n%s", diff)
		}
	})

	invalid := []struct {
		name string
		edit func(in *settings.RemoteInput)
	}{
		{"3章/接続情報/資格情報がないときに、シークレットが空なら拒否する", func(in *settings.RemoteInput) { in.SecretAccessKey = "" }},
		{"3章/接続情報/資格情報がないときに、シークレットが空白だけなら拒否する", func(in *settings.RemoteInput) { in.SecretAccessKey = " " }},
		{"3章/接続情報/エンドポイントが http か https の URL でなければ拒否する（スキームがない）", func(in *settings.RemoteInput) { in.Endpoint = "account.r2.cloudflarestorage.com" }},
		{"3章/接続情報/エンドポイントが http か https の URL でなければ拒否する（ftp）", func(in *settings.RemoteInput) { in.Endpoint = "ftp://example.com" }},
		{"3章/接続情報/エンドポイントが http か https の URL でなければ拒否する（ホストがない）", func(in *settings.RemoteInput) { in.Endpoint = "https://" }},
		{"3章/接続情報/バケットかアクセスキー ID が空なら拒否する（バケット）", func(in *settings.RemoteInput) { in.Bucket = " " }},
		{"3章/接続情報/バケットかアクセスキー ID が空なら拒否する（アクセスキー ID）", func(in *settings.RemoteInput) { in.AccessKeyID = "" }},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, nil)
			in := savedInput
			tt.edit(&in)

			err := f.settings.SaveRemote(ctx, in)
			if !errors.Is(err, settings.ErrInvalidSettings) {
				t.Fatalf("SaveRemote: err = %v, want ErrInvalidSettings", err)
			}
			if _, err := f.settings.Remote(ctx); !errors.Is(err, settings.ErrRemoteNotConfigured) {
				t.Errorf("拒否した後の Remote: err = %v, want ErrRemoteNotConfigured", err)
			}
		})
	}
}

func TestDeviceName(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		// saved は、DeviceName を呼ぶ前に SaveDeviceName で保存する値。空なら保存しない
		saved string
		want  string
	}{
		{name: "3章/deviceName/保存していなければ、ホスト名を返す", want: "host-pc"},
		{name: "3章/deviceName/保存した値を返す", saved: "Desktop", want: "Desktop"},
		{name: "3章/deviceName/前後の空白を除いて保存する", saved: "  Desktop \t", want: "Desktop"},
		{name: "3章/deviceName/64 文字までは保存できる", saved: strings.Repeat("あ", 64), want: strings.Repeat("あ", 64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, nil)
			if tt.saved != "" {
				if err := f.settings.SaveDeviceName(ctx, tt.saved); err != nil {
					t.Fatalf("SaveDeviceName: %v", err)
				}
			}

			got, err := f.settings.DeviceName(ctx)
			if err != nil {
				t.Fatalf("DeviceName: %v", err)
			}
			if got != tt.want {
				t.Errorf("DeviceName = %q, want %q", got, tt.want)
			}
		})
	}

	invalid := []struct {
		name string
		in   string
	}{
		{"3章/deviceName/空か、64 文字を超えれば拒否する（空白だけ）", " \t"},
		{"3章/deviceName/空か、64 文字を超えれば拒否する（65 文字）", strings.Repeat("あ", 65)},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, nil)

			err := f.settings.SaveDeviceName(ctx, tt.in)
			if !errors.Is(err, settings.ErrInvalidSettings) {
				t.Fatalf("SaveDeviceName: err = %v, want ErrInvalidSettings", err)
			}
			got, err := f.settings.DeviceName(ctx)
			if err != nil {
				t.Fatalf("DeviceName: %v", err)
			}
			if got != "host-pc" {
				t.Errorf("拒否した後の DeviceName = %q, want %q", got, "host-pc")
			}
		})
	}

	hostErrors := []struct {
		name     string
		hostname func() (string, error)
	}{
		{"3章/deviceName/保存していなくて、ホスト名を取れなければエラーを返す", func() (string, error) { return "", errors.New("no hostname") }},
		{"3章/deviceName/保存していなくて、ホスト名が空ならエラーを返す", func() (string, error) { return " ", nil }},
	}
	for _, tt := range hostErrors {
		t.Run(tt.name, func(t *testing.T) {
			s, err := store.Open(ctx, filepath.Join(t.TempDir(), "cloudlaunch.db"))
			if err != nil {
				t.Fatalf("store.Open: %v", err)
			}
			t.Cleanup(func() { _ = s.Close() })
			set := settings.New(settings.Config{
				Local:       s,
				Credentials: &memCredentials{},
				Getenv:      func(string) string { return "" },
				Hostname:    tt.hostname,
			})

			if _, err := set.DeviceName(ctx); err == nil {
				t.Error("DeviceName: err = nil, want error")
			}
		})
	}
}

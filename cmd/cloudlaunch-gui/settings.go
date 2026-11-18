// 設定画面（R2 の接続情報と deviceName、仕様書 3 章）。保存したら、再起動せずにコアを作り直して差し替える。
package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/fuyu28/CloudLaunch_v3/internal/settings"
)

type SettingsService struct {
	settings *settings.Settings
	sync     *SyncService
	// open は、保存した設定からコアを作り直す
	open   func(ctx context.Context) (cores, error)
	logger *slog.Logger
}

// SettingsView は、設定画面に出す値。シークレットは WebView に渡さず、保存してあるかだけを渡す。
type SettingsView struct {
	Endpoint    string `json:"endpoint"`
	Region      string `json:"region"`
	Bucket      string `json:"bucket"`
	AccessKeyID string `json:"accessKeyId"`
	SecretSaved bool   `json:"secretSaved"`
	DeviceName  string `json:"deviceName"`
	// EnvNotice は、環境変数の接続情報を使っているときに入る
	EnvNotice string `json:"envNotice"`
	// Error は、空でなければ、値を読めなかったことを出す
	Error string `json:"error"`
}

// RemoteForm は、設定画面で入れた接続情報。SecretAccessKey が空なら、保存してある値を残す。
type RemoteForm struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
}

// SaveView は、保存の結果。どちらか一方だけが入る。
type SaveView struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

func (v *SettingsService) Load(ctx context.Context) SettingsView {
	r, err := v.settings.SavedRemote(ctx)
	if err != nil {
		v.logger.Error("read settings failed", "err", err)
		return SettingsView{Error: "設定を読めませんでした: " + err.Error()}
	}
	name, err := v.settings.DeviceName(ctx)
	if err != nil {
		v.logger.Error("read device name failed", "err", err)
		return SettingsView{Error: "deviceName を読めませんでした: " + err.Error()}
	}
	view := SettingsView{
		Endpoint: r.Endpoint, Region: r.Region, Bucket: r.Bucket, AccessKeyID: r.AccessKeyID, SecretSaved: r.HasSecret,
		DeviceName: name,
	}
	if v.settings.UsesEnv() {
		view.EnvNotice = "環境変数 CLOUDLAUNCH_S3_* の接続情報を使っています。保存した接続情報は、環境変数をすべて外すまで使いません"
	}
	return view
}

func (v *SettingsService) SaveRemote(ctx context.Context, f RemoteForm) SaveView {
	return v.save(ctx, func() error {
		return v.settings.SaveRemote(ctx, settings.RemoteInput{
			Endpoint: f.Endpoint, Region: f.Region, Bucket: f.Bucket, AccessKeyID: f.AccessKeyID, SecretAccessKey: f.SecretAccessKey,
		})
	})
}

func (v *SettingsService) SaveDeviceName(ctx context.Context, name string) SaveView {
	return v.save(ctx, func() error { return v.settings.SaveDeviceName(ctx, name) })
}

// save は、確認中か転送中のゲームがあれば、何も書かずに拒否する。転送中のコアと表示が食い違わないようにするため。
// 書けなければ、コアを差し替えない。
func (v *SettingsService) save(ctx context.Context, write func() error) SaveView {
	if err := v.sync.beginReplace(); err != nil {
		return SaveView{Error: "確認中か転送中のゲームがあるので、保存できません。終わってからもう一度保存してください"}
	}
	if err := write(); err != nil {
		v.sync.endReplace(nil, nil)
		if errors.Is(err, settings.ErrInvalidSettings) {
			return SaveView{Error: "入力を確かめてください: " + err.Error()}
		}
		v.logger.Error("save settings failed", "err", err)
		return SaveView{Error: "保存できませんでした: " + err.Error()}
	}
	c, setupErr := v.open(ctx)
	v.sync.endReplace(&c, setupErr)
	if setupErr != nil {
		return SaveView{Status: "保存しました。" + setupMessage(setupErr)}
	}
	return SaveView{Status: "保存しました"}
}

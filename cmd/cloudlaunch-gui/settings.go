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
	open     func(ctx context.Context) (cores, setupErrors)
	logger   *slog.Logger
}

// SettingsView は、設定画面に出す値。シークレットは WebView に渡さず、保存してあるかだけを渡す。
type SettingsView struct {
	Endpoint    string `json:"endpoint"`
	Region      string `json:"region"`
	Bucket      string `json:"bucket"`
	AccessKeyID string `json:"accessKeyId"`
	SecretSaved bool   `json:"secretSaved"`
	DeviceName  string `json:"deviceName"`
	// RemoteError と DeviceError は、その欄の値を読めなかったときに入る。読めなくても、入れ直して保存すれば直るので、
	// 欄は空のまま出す
	RemoteError string `json:"remoteError"`
	DeviceError string `json:"deviceError"`
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
	var view SettingsView
	if r, err := v.settings.SavedRemote(ctx); err != nil {
		v.logger.Error("read settings failed", "err", err)
		view.RemoteError = "保存した接続情報を読めませんでした。入れ直して保存してください: " + err.Error()
	} else {
		view.Endpoint, view.Region, view.Bucket, view.AccessKeyID, view.SecretSaved = r.Endpoint, r.Region, r.Bucket, r.AccessKeyID, r.HasSecret
	}
	if name, err := v.settings.DeviceName(ctx); err != nil {
		v.logger.Error("read device name failed", "err", err)
		view.DeviceError = "deviceName を決められませんでした。入れて保存してください: " + err.Error()
	} else {
		view.DeviceName = name
	}
	return view
}

func (v *SettingsService) SaveRemote(ctx context.Context, f RemoteForm) SaveView {
	return v.save(ctx, func(ctx context.Context) error {
		return v.settings.SaveRemote(ctx, settings.RemoteInput{
			Endpoint: f.Endpoint, Region: f.Region, Bucket: f.Bucket, AccessKeyID: f.AccessKeyID, SecretAccessKey: f.SecretAccessKey,
		})
	})
}

func (v *SettingsService) SaveDeviceName(ctx context.Context, name string) SaveView {
	return v.save(ctx, func(ctx context.Context) error { return v.settings.SaveDeviceName(ctx, name) })
}

// save は、終了の処理を始めた後か、確認中か転送中のゲームがあれば、何も書かずに拒否する。走っている操作のコアと表示が
// 食い違わないようにするため。書けなければ、コアを差し替えない。
func (v *SettingsService) save(ctx context.Context, write func(context.Context) error) SaveView {
	// 画面の再読み込みなどで呼び出しが取り消されても、書き込みとコアの作り直しを途中で止めない。止めると、取り消しのエラーが
	// 同期の設定のエラーとして残り、保存し直すか再起動するまで確認できなくなる
	ctx = context.WithoutCancel(ctx)
	if refused := v.sync.beginReplace(); refused != "" {
		return SaveView{Error: refused}
	}
	if err := write(ctx); err != nil {
		v.sync.endReplace(nil, setupErrors{})
		if errors.Is(err, settings.ErrInvalidSettings) {
			return SaveView{Error: "入力を確かめてください: " + err.Error()}
		}
		v.logger.Error("save settings failed", "err", err)
		return SaveView{Error: "保存できませんでした: " + err.Error()}
	}
	c, setup := v.open(ctx)
	v.sync.endReplace(&c, setup)
	if !setup.ok() {
		setup.log(v.logger)
		return SaveView{Status: "保存しました。" + setupMessage(setup)}
	}
	return SaveView{Status: "保存しました"}
}

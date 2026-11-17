// GUI のエントリポイント。DB とコアを組み立て、起動時の復旧（仕様書 6 章）を済ませてから、Wails の画面を開く。
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/fuyu28/CloudLaunch_v3/internal/gamesync"
	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/objstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/platform"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

// フロントをビルドしていなくても対象があるよう、dist/.gitkeep をコミットしてある。all: がないと . で始まるファイルを含めず、
// 対象がなくてビルドできない
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run は、ログのファイルを開けた後のエラーをログにも書く。ダブルクリックで起動すると stderr が見えないため。
func run() error {
	dir, err := platform.DataDir()
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, "logs", "gui.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	if err := runApp(dir, logPath, logFile); err != nil {
		slog.New(slog.NewTextHandler(logFile, nil)).Error("gui exited", "err", err)
		return err
	}
	return nil
}

func runApp(dir, logPath string, logFile *os.File) error {
	// go build だけでは dist に .gitkeep しかなく、白い画面のバイナリが黙ってできるので、起動の前に止める
	if _, err := fs.Stat(assets, "frontend/dist/index.html"); err != nil {
		return errors.New("フロントをビルドしていません。mise run gui-build でビルドしてください")
	}
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(logFile, nil))

	s, err := store.Open(ctx, filepath.Join(dir, "cloudlaunch.db"))
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	// deviceName は DB の設定に置き、初期値をホスト名にする（仕様書 3 章）。設定画面（#142）ができるまでは初期値を渡す
	host, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("hostname: %w", err)
	}
	cfg := syncore.Config{
		Local:      s,
		Logger:     logger,
		DeviceName: host,
		BackupsDir: filepath.Join(dir, "backups"),
		Locker:     platform.NewFileLock(filepath.Join(dir, "sync.lock")),
		Processes:  platform.Processes{},
		Rename:     platform.Rename,
	}
	metaCfg := metasync.Config{Local: s, Logger: logger, ImageDir: filepath.Join(dir, "images")}
	// 接続情報がなくても一覧と起動時の復旧は使えるので、起動は止めず、確認のときに伝える。nil の *objstore.S3 を
	// インターフェースに入れると nil と比べて等しくならないので、読めたときだけ入れる
	remote, remoteErr := openRemote()
	if remoteErr == nil {
		cfg.Remote = remote
		metaCfg.Remote = remote
	}
	core := syncore.New(cfg)
	games := gamesync.New(gamesync.Config{Save: core, Meta: metasync.New(metaCfg), Logger: logger})

	// 復旧した行は Recover がログに出す
	_, err = core.Recover(ctx)
	if err != nil {
		logger.Error("recover failed", "err", err)
	}

	app := application.New(application.Options{
		Name: "CloudLaunch",
		Services: []application.Service{
			application.NewService(&HomeService{store: s, banner: recoverBanner(err, logPath)}),
			application.NewService(newSyncService(core, games, logger, remoteErr)),
		},
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		// Wails は画面のファイルの要求ごとに INFO を出し、同期のログが埋もれるので、WARN 以上だけにする
		Logger: slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "CloudLaunch",
		Width:  1000,
		Height: 700,
		URL:    "/",
	})
	return app.Run()
}

// openRemote は、CLI と同じ環境変数から接続情報を読む。設定画面と資格情報マネージャーは #142 で足す。
func openRemote() (*objstore.S3, error) {
	return objstore.New(objstore.Config{
		Endpoint:        os.Getenv("CLOUDLAUNCH_S3_ENDPOINT"),
		Region:          os.Getenv("CLOUDLAUNCH_S3_REGION"),
		Bucket:          os.Getenv("CLOUDLAUNCH_S3_BUCKET"),
		AccessKeyID:     os.Getenv("CLOUDLAUNCH_S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("CLOUDLAUNCH_S3_SECRET_ACCESS_KEY"),
	})
}

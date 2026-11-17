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

	// 復旧はリモートに触れないので、Remote と DeviceName を渡さない。復旧した行は Recover がログに出す
	core := syncore.New(syncore.Config{
		Local:      s,
		Logger:     logger,
		BackupsDir: filepath.Join(dir, "backups"),
		Locker:     platform.NewFileLock(filepath.Join(dir, "sync.lock")),
		Processes:  platform.Processes{},
		Rename:     platform.Rename,
	})
	_, err = core.Recover(ctx)
	if err != nil {
		logger.Error("recover failed", "err", err)
	}

	app := application.New(application.Options{
		Name:     "CloudLaunch",
		Services: []application.Service{application.NewService(&HomeService{store: s, banner: recoverBanner(err, logPath)})},
		Assets:   application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
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

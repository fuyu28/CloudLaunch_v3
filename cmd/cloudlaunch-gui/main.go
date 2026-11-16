// GUI のエントリポイント。DB とコアを組み立て、起動時の復旧（仕様書 6 章）を済ませてから、Wails の画面を開く。
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

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

func run() error {
	ctx := context.Background()
	dir, err := dataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o700); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "logs", "gui.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer func() { _ = logFile.Close() }()
	logger := slog.New(slog.NewTextHandler(logFile, nil))

	s, err := store.Open(ctx, filepath.Join(dir, "cloudlaunch.db"))
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	// 復旧はリモートに触れないので、Remote と DeviceName を渡さない
	core := syncore.New(syncore.Config{
		Local:      s,
		Logger:     logger,
		BackupsDir: filepath.Join(dir, "backups"),
		Locker:     platform.NewFileLock(filepath.Join(dir, "sync.lock")),
		Processes:  platform.Processes{},
		Rename:     platform.Rename,
	})
	recovered, err := core.Recover(ctx)
	for _, r := range recovered {
		logger.Info("recovered pull", "game", r.GameID, "op", r.OpID, "case", r.Case)
	}
	if err != nil {
		logger.Error("recover failed", "err", err)
	}

	app := application.New(application.Options{
		Name:     "CloudLaunch",
		Services: []application.Service{application.NewService(&HomeService{store: s, recoverFailed: err != nil})},
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

// dataDir は、CLI の既定と同じ場所を返し、同じ DB を使う。Windows では仕様書 3 章の保存先、Linux では XDG Base Directory の
// 慣例に従う。
func dataDir() (string, error) {
	var dir string
	switch {
	case runtime.GOOS == "windows":
		d := os.Getenv("LOCALAPPDATA")
		if d == "" {
			return "", errors.New("LOCALAPPDATA is not set")
		}
		dir = filepath.Join(d, "CloudLaunch")
	case os.Getenv("XDG_DATA_HOME") != "":
		dir = filepath.Join(os.Getenv("XDG_DATA_HOME"), "cloudlaunch")
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		dir = filepath.Join(home, ".local", "share", "cloudlaunch")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}
	return dir, nil
}

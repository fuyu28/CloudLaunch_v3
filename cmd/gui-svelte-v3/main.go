// GUI の試作（Svelte + Wails v3）のエントリポイント。Svelte をビルドした frontend/dist を埋め込み、App を Service として公開する。
// -tags server でビルドすると、ウィンドウを出さずに HTTP サーバとして動き、ブラウザから同じ画面を確かめられる。
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/fuyu28/CloudLaunch_v3/internal/store"
)

// frontend/dist/.gitkeep をコミットしてあるので、フロントエンドをビルドしていなくても Go の検査は通る（画面は出ない）。
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	dataDir := flag.String("data-dir", "", "データのディレクトリ（既定は CLI と同じ場所）")
	flag.Parse()

	dir := *dataDir
	if dir == "" {
		d, err := defaultDataDir()
		if err != nil {
			return err
		}
		dir = d
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	s, err := store.Open(context.Background(), filepath.Join(dir, "cloudlaunch.db"))
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	svc := &App{store: s}
	svc.sync = newFakeSync(svc.emitProgress)
	app := application.New(application.Options{
		Name:     "CloudLaunch (Svelte v3)",
		Services: []application.Service{application.NewService(svc)},
		Assets:   application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "CloudLaunch (Svelte v3)",
		Width:  1200,
		Height: 800,
		URL:    "/",
	})
	return app.Run()
}

// defaultDataDir は CLI（cmd/cloudlaunch-cli）と同じ場所を返す。試作なので共通化はしていない。
func defaultDataDir() (string, error) {
	if goruntime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "CloudLaunch"), nil
		}
		return "", errors.New("LOCALAPPDATA is not set")
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "cloudlaunch"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "cloudlaunch"), nil
}

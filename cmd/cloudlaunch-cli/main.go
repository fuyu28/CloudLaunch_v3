// cloudlaunch-cli は同期コアを動かして確かめるための CLI のエントリポイント。各パッケージを組み合わせる配線だけを書く。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/google/uuid"

	"github.com/fuyu28/CloudLaunch_v3/internal/store"
)

const usage = `usage:
  cloudlaunch-cli game add --title <タイトル> --save-dir <パス> --exe <パス> [--brand <ブランド>] [--id <gameId>] [--data-dir <dir>]`

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) >= 2 && args[0] == "game" && args[1] == "add" {
		return gameAdd(ctx, args[2:], stdout)
	}
	return errors.New(usage)
}

func gameAdd(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("game add", flag.ContinueOnError)
	title := fs.String("title", "", "ゲームのタイトル")
	brand := fs.String("brand", "", "ブランド名")
	saveDir := fs.String("save-dir", "", "セーブフォルダの絶対パス")
	exe := fs.String("exe", "", "実行ファイルの絶対パス")
	id := fs.String("id", "", "ゲーム ID（UUIDv7）。省くと新しく振る")
	dataDir := fs.String("data-dir", "", "DB などを置くディレクトリ")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *title == "" {
		return errors.New("--title is required")
	}
	// 存在は検査しない。2 台目の PC では、まだセーブフォルダがないことがある（仕様書 4 章の「local なし」）
	for _, f := range []struct{ name, path string }{{"--save-dir", *saveDir}, {"--exe", *exe}} {
		if !filepath.IsAbs(f.path) {
			return fmt.Errorf("%s must be an absolute path: %q", f.name, f.path)
		}
	}
	gameID, err := resolveGameID(*id)
	if err != nil {
		return err
	}

	s, err := openStore(ctx, *dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	if err := s.CreateGame(ctx, store.NewGame{
		ID:             gameID,
		Title:          *title,
		Brand:          *brand,
		SaveFolderPath: filepath.Clean(*saveDir),
		ExePath:        filepath.Clean(*exe),
		CreatedAt:      time.Now(),
	}); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, gameID)
	return err
}

func resolveGameID(id string) (string, error) {
	if id == "" {
		u, err := uuid.NewV7()
		if err != nil {
			return "", fmt.Errorf("generate game id: %w", err)
		}
		return u.String(), nil
	}
	u, err := uuid.Parse(id)
	if err != nil || u.Version() != 7 {
		return "", fmt.Errorf("--id must be a UUIDv7: %q", id)
	}
	return u.String(), nil
}

func openStore(ctx context.Context, dataDir string) (*store.Store, error) {
	if dataDir == "" {
		d, err := defaultDataDir()
		if err != nil {
			return nil, err
		}
		dataDir = d
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	return store.Open(ctx, filepath.Join(dataDir, "cloudlaunch.db"))
}

// defaultDataDir は Windows では仕様書 3 章の保存先、Linux では XDG Base Directory の慣例に従う。
func defaultDataDir() (string, error) {
	if runtime.GOOS == "windows" {
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

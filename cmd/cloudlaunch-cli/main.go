// cloudlaunch-cli は同期コアを動かして確かめるための CLI のエントリポイント。各パッケージを組み合わせる配線だけを書く。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/google/uuid"

	"github.com/fuyu28/CloudLaunch_v3/internal/objstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

const usage = `usage:
  cloudlaunch-cli game add --title <タイトル> --save-dir <パス> --exe <パス> [--brand <ブランド>] [--id <gameId>] [--data-dir <dir>]
  cloudlaunch-cli status --game <gameId> [--data-dir <dir>]
  cloudlaunch-cli push --game <gameId> [--data-dir <dir>]
  cloudlaunch-cli pull --game <gameId> [--data-dir <dir>]

status・push・pull はリモートの接続先と鍵を環境変数から読む:
  CLOUDLAUNCH_S3_ENDPOINT, CLOUDLAUNCH_S3_BUCKET, CLOUDLAUNCH_S3_ACCESS_KEY_ID, CLOUDLAUNCH_S3_SECRET_ACCESS_KEY,
  CLOUDLAUNCH_S3_REGION（省くと auto）`

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
	if len(args) >= 1 && args[0] == "status" {
		return status(ctx, args[1:], stdout)
	}
	if len(args) >= 1 && args[0] == "push" {
		return push(ctx, args[1:], stdout)
	}
	if len(args) >= 1 && args[0] == "pull" {
		return pull(ctx, args[1:], stdout)
	}
	return errors.New(usage)
}

func status(ctx context.Context, args []string, stdout io.Writer) error {
	core, gameID, closeStore, err := openCore(ctx, "status", args)
	if err != nil {
		return err
	}
	defer closeStore()

	plan, err := core.Status(ctx, gameID)
	if err != nil {
		return err
	}
	if err := printPlan(stdout, plan); err != nil {
		return err
	}
	return recordBaseIfNeeded(ctx, core, plan, stdout)
}

// recordBaseIfNeeded は、データを転送しないので確認を取らない（仕様書 4 章）。
func recordBaseIfNeeded(ctx context.Context, core *syncore.Core, plan syncore.Plan, stdout io.Writer) error {
	if !plan.RecordBase {
		return nil
	}
	if err := core.RecordBase(ctx, plan); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, recordBaseLabel(plan)+"しました")
	return err
}

// push は、コマンドの実行を承認とみなして確認を出さない（仕様書 7 章の確認は GUI が出す）。
func push(ctx context.Context, args []string, stdout io.Writer) error {
	core, gameID, closeStore, err := openCore(ctx, "push", args)
	if err != nil {
		return err
	}
	defer closeStore()

	plan, err := core.Status(ctx, gameID)
	if err != nil {
		return err
	}
	if err := printPlan(stdout, plan); err != nil {
		return err
	}
	if plan.Decision != syncore.DecisionPush {
		if _, err := fmt.Fprintln(stdout, "判定が Push でないので、アップロードしません"); err != nil {
			return err
		}
		return recordBaseIfNeeded(ctx, core, plan, stdout)
	}
	if err := core.Push(ctx, plan); err != nil {
		if errors.Is(err, syncore.ErrPlanStale) {
			return fmt.Errorf("状況が変わりました。もう一度実行してください: %w", err)
		}
		return err
	}
	_, err = fmt.Fprintln(stdout, "アップロードしました")
	return err
}

// pull は、push と同じくコマンドの実行を承認とみなして確認を出さない。
func pull(ctx context.Context, args []string, stdout io.Writer) error {
	core, gameID, closeStore, err := openCore(ctx, "pull", args)
	if err != nil {
		return err
	}
	defer closeStore()

	plan, err := core.Status(ctx, gameID)
	if err != nil {
		return err
	}
	if err := printPlan(stdout, plan); err != nil {
		return err
	}
	if plan.Decision != syncore.DecisionPull {
		if _, err := fmt.Fprintln(stdout, "判定が Pull でないので、ダウンロードしません"); err != nil {
			return err
		}
		return recordBaseIfNeeded(ctx, core, plan, stdout)
	}
	if err := core.Pull(ctx, plan); err != nil {
		if errors.Is(err, syncore.ErrPlanStale) {
			return fmt.Errorf("状況が変わりました。もう一度実行してください: %w", err)
		}
		return err
	}
	_, err = fmt.Fprintln(stdout, "ダウンロードしました")
	return err
}

// openCore は --game と --data-dir を読み、リモートと DB につないだコアを返す。closeStore は呼び出し側が必ず呼ぶ。
func openCore(ctx context.Context, name string, args []string) (core *syncore.Core, gameID string, closeStore func(), err error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	game := fs.String("game", "", "ゲーム ID")
	dataDir := fs.String("data-dir", "", "DB などを置くディレクトリ")
	if err := fs.Parse(args); err != nil {
		return nil, "", nil, err
	}
	if *game == "" {
		return nil, "", nil, errors.New("--game is required")
	}

	remote, err := objstore.New(objstore.Config{
		Endpoint:        os.Getenv("CLOUDLAUNCH_S3_ENDPOINT"),
		Region:          os.Getenv("CLOUDLAUNCH_S3_REGION"),
		Bucket:          os.Getenv("CLOUDLAUNCH_S3_BUCKET"),
		AccessKeyID:     os.Getenv("CLOUDLAUNCH_S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("CLOUDLAUNCH_S3_SECRET_ACCESS_KEY"),
	})
	if err != nil {
		return nil, "", nil, err
	}
	// deviceName は DB の設定に置き、初期値をホスト名にする（仕様書 3 章）。変更する手段ができるまでは初期値をそのまま渡す
	host, err := os.Hostname()
	if err != nil {
		return nil, "", nil, fmt.Errorf("hostname: %w", err)
	}
	s, dir, err := openStore(ctx, *dataDir)
	if err != nil {
		return nil, "", nil, err
	}

	core = syncore.New(syncore.Config{
		Remote:     remote,
		Local:      s,
		Logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
		DeviceName: host,
		BackupsDir: filepath.Join(dir, "backups"),
	})
	return core, *game, func() { _ = s.Close() }, nil
}

func printPlan(w io.Writer, plan syncore.Plan) error {
	decision := map[syncore.Decision]string{
		syncore.DecisionNoChange: "変更なし",
		syncore.DecisionPush:     "Push",
		syncore.DecisionPull:     "Pull",
		syncore.DecisionConflict: "競合",
	}[plan.Decision]
	if plan.RecordBase {
		decision += "（" + recordBaseLabel(plan) + "）"
	}
	if plan.LocalMissing {
		decision += "（ローカルにセーブが見つかりません）"
	}
	_, err := fmt.Fprintf(w, "判定: %s\nbase: %s\nlocal: %s\nremote: %s\n",
		decision, orDash(plan.Base), orDash(plan.Local.Hash), orDash(plan.Remote.Hash))
	return err
}

func recordBaseLabel(plan syncore.Plan) string {
	if plan.Base == "" {
		return "base を記録"
	}
	return "base を更新"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
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

	s, _, err := openStore(ctx, *dataDir)
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

// openStore は、dataDir が空なら既定の場所を使い、実際に使ったディレクトリも返す。
func openStore(ctx context.Context, dataDir string) (*store.Store, string, error) {
	if dataDir == "" {
		d, err := defaultDataDir()
		if err != nil {
			return nil, "", err
		}
		dataDir = d
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create data dir: %w", err)
	}
	s, err := store.Open(ctx, filepath.Join(dataDir, "cloudlaunch.db"))
	return s, dataDir, err
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

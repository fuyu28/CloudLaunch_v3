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
	"github.com/fuyu28/CloudLaunch_v3/internal/settings"
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

	set := settings.New(settings.Config{Local: s, Credentials: platform.NewCredentialStore(platform.R2CredentialTarget, dir)})
	open := func(ctx context.Context) (cores, setupErrors) { return openCores(ctx, dir, s, logger, set) }
	c, setup := open(ctx)
	setup.log(logger)

	// 復旧した行は Recover がログに出す
	_, err = c.save.Recover(ctx)
	if err != nil {
		logger.Error("recover failed", "err", err)
	}

	// バックアップの置き場所は設定で変わらないので、BackupService のコアは設定を保存しても作り直さない
	backups := &BackupService{save: c.save}
	// 画像のキャッシュの置き場所も設定で変わらないので、登録の画面のコアはリモートにつながずに 1 回だけ作る
	games := &GameService{store: s, meta: metasync.New(metasync.Config{Local: s, Logger: logger, ImageDir: imageDir(dir)}), logger: logger}
	syncs := newSyncService(c, logger, setup)
	app := application.New(application.Options{
		Name: "CloudLaunch",
		Services: []application.Service{
			application.NewService(&HomeService{store: s, sync: syncs, banner: recoverBanner(err, logPath)}),
			application.NewService(syncs),
			application.NewService(backups),
			application.NewService(games),
			application.NewService(&SettingsService{settings: set, sync: syncs, open: open, logger: logger}),
		},
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		// Wails は画面のファイルの要求ごとに INFO を出し、同期のログが埋もれるので、WARN 以上だけにする
		Logger: slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	backups.openFolder = func(dir string) error { return app.Env.OpenFileManager(dir, false) }
	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "CloudLaunch",
		Width:  1000,
		Height: 700,
		URL:    "/",
	})
	games.pick = func(opts *application.OpenFileDialogOptions) (string, error) {
		// 窓に結び付けないと、ダイアログが窓の裏に開くことがある
		opts.Window = win
		return app.Dialog.OpenFileWithOptions(opts).PromptForSingleSelection()
	}
	return app.Run()
}

// cores は、接続情報と deviceName から組み立てるコア。設定を保存するたびに作り直す。
type cores struct {
	save  *syncore.Core
	games *gamesync.Core
}

// setupErrors は、コアを用意できなかった理由を、接続情報と deviceName に分けて持つ。errors.Join でまとめると、帯で
// ErrRemoteNotConfigured の案内だけを出して deviceName のエラーを落とし、保存するたびに直す箇所が 1 つずつしか分からないため。
type setupErrors struct {
	remote error
	device error
}

func (e setupErrors) ok() bool { return e.remote == nil && e.device == nil }

func (e setupErrors) log(logger *slog.Logger) {
	if !e.ok() {
		logger.Warn("sync is not set up", "remote", e.remote, "device", e.device)
	}
}

// openCores は、接続情報か deviceName を読めなくても、リモートにつながないコアとその理由を返す。一覧と起動時の復旧は
// 使えるので起動は止めず、確認のときに伝える。
func openCores(ctx context.Context, dir string, s *store.Store, logger *slog.Logger, set *settings.Settings) (cores, setupErrors) {
	var remote *objstore.S3
	r, remoteErr := set.Remote(ctx)
	if remoteErr == nil {
		remote, remoteErr = objstore.New(objstore.Config{
			Endpoint: r.Endpoint, Region: r.Region, Bucket: r.Bucket, AccessKeyID: r.AccessKeyID, SecretAccessKey: r.SecretAccessKey,
		})
	}
	deviceName, deviceErr := set.DeviceName(ctx)

	cfg := syncore.Config{
		Local:      s,
		Logger:     logger,
		DeviceName: deviceName,
		BackupsDir: filepath.Join(dir, "backups"),
		Locker:     platform.NewFileLock(filepath.Join(dir, "sync.lock")),
		Processes:  platform.Processes{},
		Rename:     platform.Rename,
	}
	metaCfg := metasync.Config{Local: s, Logger: logger, ImageDir: imageDir(dir)}
	// nil の *objstore.S3 をインターフェースに入れると nil と比べて等しくならないので、つないだときだけ入れる
	if remoteErr == nil {
		cfg.Remote = remote
		metaCfg.Remote = remote
	}
	save := syncore.New(cfg)
	games := gamesync.New(gamesync.Config{Save: save, Meta: metasync.New(metaCfg), Logger: logger})
	return cores{save: save, games: games}, setupErrors{remote: remoteErr, device: deviceErr}
}

// imageDir は、同期のコアと登録の画面のコアで、画像のキャッシュの置き場所を食い違わせないために 1 か所で決める。
func imageDir(dataDir string) string { return filepath.Join(dataDir, "images") }

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
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/fuyu28/CloudLaunch_v3/internal/legacy"
	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/objstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/platform"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

const usage = `usage:
  cloudlaunch-cli game add --title <タイトル> --save-dir <パス> --exe <パス> [--brand <ブランド>] [--id <gameId>] [--data-dir <dir>]
  cloudlaunch-cli game edit --game <gameId> [--title <タイトル>] [--brand <ブランド>] [--data-dir <dir>]
  cloudlaunch-cli game set-image --game <gameId> (--file <パス> | --clear) [--data-dir <dir>]
  cloudlaunch-cli game remote [--data-dir <dir>]
  cloudlaunch-cli game import --game <gameId> --save-dir <パス> --exe <パス> [--data-dir <dir>]
  cloudlaunch-cli game delete --game <gameId> [--remote --confirm-title <タイトル>] [--data-dir <dir>]
  cloudlaunch-cli session add --game <gameId> --duration <秒> [--played-at <RFC3339>] [--data-dir <dir>]
  cloudlaunch-cli legacy import [--data-dir <dir>] <旧版のエクスポートの JSON>
  cloudlaunch-cli legacy link --game <gameId> --legacy-game <旧版の gameId> [--data-dir <dir>]
  cloudlaunch-cli status --game <gameId> [--data-dir <dir>]
  cloudlaunch-cli push --game <gameId> [--overwrite] [--data-dir <dir>]
  cloudlaunch-cli pull --game <gameId> [--overwrite] [--data-dir <dir>]
  cloudlaunch-cli recover [--data-dir <dir>]

status・push・pull・game remote・game import・game delete --remote はリモートの接続先と鍵を環境変数から読む:
  CLOUDLAUNCH_S3_ENDPOINT, CLOUDLAUNCH_S3_BUCKET, CLOUDLAUNCH_S3_ACCESS_KEY_ID, CLOUDLAUNCH_S3_SECRET_ACCESS_KEY,
  CLOUDLAUNCH_S3_REGION（省くと auto）`

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "game":
		if len(args) >= 2 && args[1] == "add" {
			return gameAdd(ctx, args[2:], stdout)
		}
		if len(args) >= 2 && args[1] == "edit" {
			return gameEdit(ctx, args[2:], stdout)
		}
		if len(args) >= 2 && args[1] == "set-image" {
			return gameSetImage(ctx, args[2:], stdout)
		}
		if len(args) >= 2 && args[1] == "remote" {
			return gameRemote(ctx, args[2:], stdout)
		}
		if len(args) >= 2 && args[1] == "import" {
			return gameImport(ctx, args[2:], stdout)
		}
		if len(args) >= 2 && args[1] == "delete" {
			return gameDelete(ctx, args[2:], stdout)
		}
	case "session":
		if len(args) >= 2 && args[1] == "add" {
			return sessionAdd(ctx, args[2:], stdout)
		}
	case "legacy":
		if len(args) >= 2 && args[1] == "import" {
			return legacyImport(ctx, args[2:], stdout)
		}
		if len(args) >= 2 && args[1] == "link" {
			return legacyLink(ctx, args[2:], stdout)
		}
	case "status":
		return status(ctx, args[1:], stdout)
	case "push":
		return transfer(ctx, args[1:], stdout, pushCommand)
	case "pull":
		return transfer(ctx, args[1:], stdout, pullCommand)
	case "recover":
		return recoverPulls(ctx, args[1:], stdout)
	}
	return errors.New(usage)
}

// recoverPulls は、復旧できた行を出してから、失敗した行のエラーを返す。リモートには触れないので、接続先を読まない。
func recoverPulls(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("recover", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "DB などを置くディレクトリ")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, dir, err := openStore(ctx, *dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	recovered, err := syncore.New(localConfig(s, dir)).Recover(ctx)
	for _, r := range recovered {
		if _, werr := fmt.Fprintf(stdout, "%s: game %s, op %s\n", recoveryLabel(r.Case), r.GameID, r.OpID); werr != nil {
			return errors.Join(err, werr)
		}
	}
	if err != nil {
		return explainSyncError(err)
	}
	if len(recovered) == 0 {
		_, err = fmt.Fprintln(stdout, "復旧する Pull はありません")
	}
	return err
}

func status(ctx context.Context, args []string, stdout io.Writer) error {
	c, err := openCore(ctx, "status", args, nil)
	if err != nil {
		return err
	}
	defer c.close()

	plan, err := c.sync.Status(ctx, c.gameID)
	if err != nil {
		return err
	}
	// status は表示だけを行い、記録も削除もしない（仕様書 8-T2）
	if plan.RemoteDeleted != nil {
		_, err := fmt.Fprintln(stdout, remoteDeletedLabel(metasync.Tombstone(*plan.RemoteDeleted)))
		return err
	}
	if err := printPlan(stdout, plan); err != nil {
		return err
	}
	return recordBaseIfNeeded(ctx, c.sync, plan, stdout)
}

func remoteDeletedLabel(t metasync.Tombstone) string {
	return fmt.Sprintf("リモートで削除されました（deviceName: %q, deletedAt: %s）", t.DeviceName, t.DeletedAt.Format(time.RFC3339Nano))
}

// errRemoteDeleted は、push・pull がゲームの tombstone で止まったことを表す。頼まれた同期をしていないので、エラーで終わる。
var errRemoteDeleted = errors.New("リモートで削除されたゲームなので、同期しませんでした")

// handleTombstone は、ゲームの tombstone を確かめ、あれば DB に記録して配下を削除し（仕様書 8-T2）、表示して
// errRemoteDeleted を返す。配下の削除に失敗しても、記録はしたので表示する。なければ nil を返す。
func handleTombstone(ctx context.Context, c cores, stdout io.Writer) error {
	t, deleted, err := c.meta.HandleTombstone(ctx, c.gameID)
	if !deleted {
		return err
	}
	if _, werr := fmt.Fprintln(stdout, remoteDeletedLabel(t)); werr != nil {
		return errors.Join(err, werr)
	}
	return errors.Join(errRemoteDeleted, err)
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

// transferCommand は、push と pull で違う部分。
type transferCommand struct {
	name string
	want syncore.Decision
	// skipped は、判定が want でなく、上書きでもないときに出す
	skipped string
	done    string
	exec    func(ctx context.Context, core *syncore.Core, plan syncore.Plan, overwrite bool) error
}

var (
	pushCommand = transferCommand{
		name:    "push",
		want:    syncore.DecisionPush,
		skipped: "判定が Push でないので、アップロードしません（上書きするときは --overwrite を付ける）",
		done:    "アップロードしました",
		exec: func(ctx context.Context, core *syncore.Core, plan syncore.Plan, overwrite bool) error {
			return core.Push(ctx, plan, syncore.PushOptions{Overwrite: overwrite})
		},
	}
	pullCommand = transferCommand{
		name:    "pull",
		want:    syncore.DecisionPull,
		skipped: "判定が Pull でないので、ダウンロードしません（上書きするときは --overwrite を付ける）",
		done:    "ダウンロードしました",
		exec: func(ctx context.Context, core *syncore.Core, plan syncore.Plan, overwrite bool) error {
			return core.Pull(ctx, plan, syncore.PullOptions{Overwrite: overwrite})
		},
	}
)

// transfer は push と pull を行う。コマンドの実行を承認とみなして確認を出さない（仕様書 7 章の確認は GUI が出す）。
// --overwrite を付けると、判定によらず上書きする。最初にゲームの tombstone を確かめ、あればどちらも同期しない（仕様書 8-M1）。
// セーブの同期が見送り・失敗で終わっても、そのあとにメタ情報を同期する（8-M2）。メタ情報の同期の失敗は、返すエラーに
// 含めない（8-M3）。
func transfer(ctx context.Context, args []string, stdout io.Writer, cmd transferCommand) error {
	var overwrite bool
	c, err := openCore(ctx, cmd.name, args, &overwrite)
	if err != nil {
		return err
	}
	defer c.close()

	if err := handleTombstone(ctx, c, stdout); err != nil {
		return err
	}
	err = syncSave(ctx, c.sync, c.gameID, stdout, cmd, overwrite)
	if err != nil {
		// 確かめた後にゲームが削除されると、セーブの同期は 8-T7 のほか、削除で消えた object を取得できずにも失敗する。
		// どちらでも、メタ情報を同期する前に tombstone を確かめ直す。確かめずに同期すると、削除したデータをリモートに戻す。
		// 確かめ直しに失敗したときも、メタ情報は同期しない
		// 元のエラーも返す。8-T7 で止まった Pull は、stage やジャーナルを削除できなかったことも含みうる
		if herr := handleTombstone(ctx, c, stdout); herr != nil {
			return errors.Join(herr, err)
		}
		if errors.Is(err, syncore.ErrRemoteDeleted) {
			return err
		}
	}
	return errors.Join(err, syncMeta(ctx, c, stdout))
}

// syncMeta は、標準出力への書き込みに失敗したときだけエラーを返す。
func syncMeta(ctx context.Context, c cores, stdout io.Writer) error {
	res, err := c.meta.Sync(ctx, c.gameID)
	if err != nil {
		c.logger.Warn("metadata sync failed", "gameId", c.gameID, "err", err)
		_, werr := fmt.Fprintf(stdout, "メタ情報の同期に失敗しました: %v\n", err)
		return werr
	}
	_, err = fmt.Fprintf(stdout, "ゲーム情報: %s\n画像: %s\nプレイ履歴を %d 件アップロードし、%d 件取り込みました\n",
		gameInfoLabel(res.GameInfo), imageLabel(res), res.UploadedSessions, res.DownloadedSessions)
	return err
}

// imageLabel は、game.json が不正で画像を扱わなかったときに「なし」と出さない。DB には画像があるので、外れたと誤解させるため。
func imageLabel(res metasync.Result) string {
	if res.GameInfo == metasync.GameInfoInvalid {
		return "ゲーム情報を同期しなかったので扱いませんでした"
	}
	switch res.Image {
	case metasync.ImageUnchanged:
		return "変更なし"
	case metasync.ImageUploaded:
		return "アップロードしました"
	case metasync.ImageDownloaded:
		return "取得しました"
	case metasync.ImageInvalid:
		return "リモートの画像が不正なので取得しませんでした"
	case metasync.ImageMissing:
		return "リモートにもキャッシュにもないので同期しませんでした"
	default:
		return "なし"
	}
}

func gameInfoLabel(o metasync.GameInfoOutcome) string {
	switch o {
	case metasync.GameInfoUploaded:
		return "アップロードしました"
	case metasync.GameInfoDownloaded:
		return "取り込みました"
	case metasync.GameInfoInvalid:
		return "リモートの game.json が不正なので同期しませんでした"
	default:
		return "変更なし"
	}
}

func syncSave(ctx context.Context, core *syncore.Core, gameID string, stdout io.Writer, cmd transferCommand, overwrite bool) error {
	plan, err := core.Status(ctx, gameID)
	if err != nil {
		return err
	}
	if plan.RemoteDeleted != nil {
		return syncore.ErrRemoteDeleted
	}
	if err := printPlan(stdout, plan); err != nil {
		return err
	}
	if plan.Decision != cmd.want && !overwrite {
		if _, err := fmt.Fprintln(stdout, cmd.skipped); err != nil {
			return err
		}
		return recordBaseIfNeeded(ctx, core, plan, stdout)
	}
	if err := cmd.exec(ctx, core, plan, overwrite); err != nil {
		return explainSyncError(err)
	}
	return printTransferred(stdout, plan, overwrite, cmd.done)
}

// printTransferred は、上書きで local と remote が同じだったときは、コアが転送せずに base だけを記録している
// （仕様書 7-O5）ので、転送したとは出さない。
func printTransferred(w io.Writer, plan syncore.Plan, overwrite bool, done string) error {
	if !overwrite || !plan.SameContent() {
		_, err := fmt.Fprintln(w, done)
		return err
	}
	msg := "local と remote が同じなので、転送しませんでした"
	if plan.RecordBase {
		msg += "（" + recordBaseLabel(plan) + "しました）"
	}
	_, err := fmt.Fprintln(w, msg)
	return err
}

// cores は、リモートと DB につないだ同期コアとメタ情報の同期。close は呼び出し側が必ず呼ぶ。
type cores struct {
	sync       *syncore.Core
	meta       *metasync.Core
	store      *store.Store
	logger     *slog.Logger
	gameID     string
	deviceName string
	close      func()
}

// openCore は --game と --data-dir を読み、リモートと DB につないだコアを返す。overwrite が nil でなければ、
// --overwrite も読む。
func openCore(ctx context.Context, name string, args []string, overwrite *bool) (cores, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	game := fs.String("game", "", "ゲーム ID")
	dataDir := fs.String("data-dir", "", "DB などを置くディレクトリ")
	if overwrite != nil {
		fs.BoolVar(overwrite, "overwrite", false, "判定によらず上書きする")
	}
	if err := fs.Parse(args); err != nil {
		return cores{}, err
	}
	if *game == "" {
		return cores{}, errors.New("--game is required")
	}
	return connectCores(ctx, *game, *dataDir)
}

// connectCores は、リモートと DB につないだコアを返す。
func connectCores(ctx context.Context, gameID, dataDir string) (cores, error) {
	remote, err := openRemote()
	if err != nil {
		return cores{}, err
	}
	// deviceName は DB の設定に置き、初期値をホスト名にする（仕様書 3 章）。変更する手段ができるまでは初期値をそのまま渡す
	host, err := os.Hostname()
	if err != nil {
		return cores{}, fmt.Errorf("hostname: %w", err)
	}
	s, dir, err := openStore(ctx, dataDir)
	if err != nil {
		return cores{}, err
	}

	cfg := localConfig(s, dir)
	meta := newMeta(remote, s, cfg.Logger, dir)
	cfg.Remote = remote
	cfg.DeviceName = host
	cfg.Processes = platform.Processes{}
	cfg.ReadTombstone = func(ctx context.Context, gameID string) (syncore.Tombstone, bool, error) {
		t, ok, err := meta.CheckTombstone(ctx, gameID)
		return syncore.Tombstone(t), ok, err
	}
	return cores{
		sync:       syncore.New(cfg),
		meta:       meta,
		store:      s,
		logger:     cfg.Logger,
		gameID:     gameID,
		deviceName: host,
		close:      func() { _ = s.Close() },
	}, nil
}

func openRemote() (*objstore.S3, error) {
	return objstore.New(objstore.Config{
		Endpoint:        os.Getenv("CLOUDLAUNCH_S3_ENDPOINT"),
		Region:          os.Getenv("CLOUDLAUNCH_S3_REGION"),
		Bucket:          os.Getenv("CLOUDLAUNCH_S3_BUCKET"),
		AccessKeyID:     os.Getenv("CLOUDLAUNCH_S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("CLOUDLAUNCH_S3_SECRET_ACCESS_KEY"),
	})
}

// openMeta は、syncore を使わない game remote・game import のために、メタ情報の同期だけをリモートと DB につなぐ。
// 返す store は、game import の後の照合（仕様書 9-K4）に使う。store は呼び出し側が必ず閉じる。
func openMeta(ctx context.Context, dataDir string) (*metasync.Core, *store.Store, error) {
	remote, err := openRemote()
	if err != nil {
		return nil, nil, err
	}
	s, dir, err := openStore(ctx, dataDir)
	if err != nil {
		return nil, nil, err
	}
	return newMeta(remote, s, newLogger(), dir), s, nil
}

func newMeta(remote *objstore.S3, s *store.Store, logger *slog.Logger, dataDir string) *metasync.Core {
	return metasync.New(metasync.Config{Remote: remote, Local: s, Logger: logger, ImageDir: imageDir(dataDir)})
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

// gameRemote は、game import に渡す gameId を cut などで取り出せるよう、タブ区切りで出す。title と brand はほかの PC が
// 書いた値で、タブ・改行・端末の制御文字を含みうるので、Go の引用符つきの表記にして列と行を崩さない。
func gameRemote(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("game remote", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "DB などを置くディレクトリ")
	if err := fs.Parse(args); err != nil {
		return err
	}
	meta, s, err := openMeta(ctx, *dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	res, err := meta.RemoteGames(ctx)
	if err != nil {
		return err
	}
	for _, g := range res.Games {
		if _, err := fmt.Fprintf(stdout, "%s\t%q\t%s\t%s\n",
			g.ID, g.Title, quoteOrDash(g.Brand), g.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	if len(res.Games) == 0 {
		if _, err := fmt.Fprintln(stdout, "取り込めるゲームはありません"); err != nil {
			return err
		}
	}
	if res.Skipped > 0 {
		_, err = fmt.Fprintf(stdout, "取り込めないゲームが %d 件あります（詳細はログ）\n", res.Skipped)
	}
	return err
}

func quoteOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return strconv.Quote(s)
}

// gameImport は、画像とプレイ履歴を取得しない。次の push・pull のメタ情報の同期で取得する（仕様書 8-N12）。
func gameImport(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("game import", flag.ContinueOnError)
	game := fs.String("game", "", "取り込むゲームの ID（game remote で確かめる）")
	saveDir := fs.String("save-dir", "", "セーブフォルダの絶対パス")
	exe := fs.String("exe", "", "実行ファイルの絶対パス")
	dataDir := fs.String("data-dir", "", "DB などを置くディレクトリ")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *game == "" {
		return errors.New("--game is required")
	}
	if err := checkAbsPaths(*saveDir, *exe); err != nil {
		return err
	}
	meta, s, err := openMeta(ctx, *dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	g, err := meta.ImportGame(ctx, *game, filepath.Clean(*saveDir), filepath.Clean(*exe))
	if err != nil {
		return err
	}
	// title はほかの PC が書いた値なので、game remote と同じく引用符つきで出す
	if _, err := fmt.Fprintf(stdout, "%s\t%q\n", g.ID, g.Title); err != nil {
		return err
	}
	// 読み直した game.json のタイトルで照合する
	return printLegacyMatch(ctx, s, g.ID, g.Title, *dataDir, stdout)
}

// gameDelete は、--remote がなければ登録だけを解除し、リモートには接続しない（仕様書 8-X1）。--remote なら、--confirm-title を
// 強い確認（8-X4）とみなし、DB のタイトルと完全に一致しなければ、何も変えずに拒否する。gameId の打ち間違いで別のゲームを
// 消さないため。
func gameDelete(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("game delete", flag.ContinueOnError)
	game := fs.String("game", "", "削除するゲームの ID")
	remote := fs.Bool("remote", false, "リモートのデータも削除する")
	confirmTitle := fs.String("confirm-title", "", "--remote で、確認のために打つゲームのタイトル")
	dataDir := fs.String("data-dir", "", "DB などを置くディレクトリ")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *game == "" {
		return errors.New("--game is required")
	}
	confirmGiven := false
	fs.Visit(func(f *flag.Flag) { confirmGiven = confirmGiven || f.Name == "confirm-title" })
	if !*remote {
		if confirmGiven {
			return errors.New("--confirm-title is only for --remote")
		}
		return unregisterGame(ctx, *game, *dataDir, stdout)
	}
	if !confirmGiven {
		return errors.New("--remote requires --confirm-title")
	}

	c, err := connectCores(ctx, *game, *dataDir)
	if err != nil {
		return err
	}
	defer c.close()
	info, err := c.store.GameInfo(ctx, c.gameID)
	if err != nil {
		return err
	}
	if info.Title != *confirmTitle {
		return fmt.Errorf("--confirm-title がゲームのタイトル %q と一致しないので、削除しません", info.Title)
	}

	res, err := c.sync.DeleteGame(ctx, c.gameID, func(ctx context.Context) error {
		return c.meta.DeleteRemoteGame(ctx, c.gameID, metasync.Tombstone{DeletedAt: time.Now(), DeviceName: c.deviceName})
	})
	if err != nil {
		return explainDeleteError(err, c.gameID, *dataDir)
	}
	backup := "セーブフォルダがないので、バックアップは作っていません"
	if res.BackupPath != "" {
		backup = "バックアップは " + res.BackupPath + " にあります"
	}
	_, err = fmt.Fprintf(stdout, "リモートのデータを削除し、この PC の登録を解除しました（セーブフォルダはそのままです。%s）\n", backup)
	return err
}

func unregisterGame(ctx context.Context, gameID, dataDir string, stdout io.Writer) error {
	s, _, err := openStore(ctx, dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	if err := s.DeleteGame(ctx, gameID); err != nil {
		return explainDeleteError(err, gameID, dataDir)
	}
	_, err = fmt.Fprintln(stdout, "この PC の登録を解除しました（セーブフォルダとリモートはそのままです）")
	return err
}

// explainDeleteError は、ゲームの削除が止まった場所（仕様書 8-X8〜8-X10）ごとに、利用者がすることを添える。
func explainDeleteError(err error, gameID, dataDir string) error {
	unregister := "cloudlaunch-cli game delete" + dataDirFlag(dataDir) + " --game " + gameID
	recoverCmd := "cloudlaunch-cli recover" + dataDirFlag(dataDir)
	switch {
	case errors.Is(err, syncore.ErrUnregisterFailed):
		return fmt.Errorf("リモートのデータは削除しましたが、この PC の登録を解除できませんでした。%s で解除してください: %w", unregister, err)
	case errors.Is(err, metasync.ErrDeleteIncomplete):
		return fmt.Errorf("リモートでは削除済みになりましたが、途中で止まりました。消し残りは次の push・pull で削除されます。この PC の登録は残っているので、%s で解除してください: %w", unregister, err)
	case errors.Is(err, syncore.ErrRemoteDeletionFailed):
		return fmt.Errorf("リモートの削除に失敗しました。削除が届いたかは分かりません。status で確かめてから、もう一度実行してください: %w", err)
	case errors.Is(err, syncore.ErrRemoteDeleted):
		return fmt.Errorf("このゲームはリモートで削除済みです。この PC の登録を解除するときは %s を実行してください: %w", unregister, err)
	case errors.Is(err, syncore.ErrPullPending), errors.Is(err, store.ErrPullInProgress):
		return fmt.Errorf("途中で止まった Pull が残っています。%s を実行してから、もう一度実行してください: %w", recoverCmd, err)
	}
	return explainSyncError(err)
}

// printLegacyMatch は、登録したゲームと未紐付けの旧履歴の照合の結果を出す（仕様書 9-K1・9-K2）。CLI は対話をしないので、
// 9-K1 の確認は、提案を見て legacy link を実行することで取る。旧版のタイトルは手で書き換えたエクスポートから来ることがあり、
// タブや改行を含みうるので、引用符つきで出して列と行を崩さない。旧版の gameId も、読み込みでは空文字しか拒否していないので、
// 同じく引用符つきで出す。コマンドの例は、引用符のまま貼り付けて実行できる。
//
// 照合に失敗しても、警告を stderr に出して nil を返す。登録は済んでいるので、失敗で終わると打ち直されて、--id を省いた
// game add では同じゲームが 2 つ登録される。照合は legacy link で後からやり直せる。
func printLegacyMatch(ctx context.Context, s *store.Store, gameID, title, dataDir string, stdout io.Writer) error {
	m, err := legacy.Match(ctx, s, title)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: ゲームは登録しましたが、旧版のプレイ履歴との照合に失敗しました:", err)
		return nil
	}
	if len(m.Candidates) == 0 {
		return nil
	}
	legacyGameID := "<旧版の gameId>"
	var others []legacy.Game
	if m.Suggested != nil {
		legacyGameID = strconv.Quote(m.Suggested.ID)
		if err := printLegacyGames(stdout, "旧版のプレイ履歴に、タイトルが一致するゲームがあります:", []legacy.Game{*m.Suggested}); err != nil {
			return err
		}
		// 提案を断って別の旧ゲームを選べるよう、ほかの候補も出す
		for _, c := range m.Candidates {
			if c.ID != m.Suggested.ID {
				others = append(others, c)
			}
		}
		if err := printLegacyGames(stdout, "ほかの候補:", others); err != nil {
			return err
		}
	} else if err := printLegacyGames(stdout, "旧版のプレイ履歴に、タイトルが一致するゲームがないか、複数あります。候補:", m.Candidates); err != nil {
		return err
	}
	// 照合に使った DB と同じ DB で取り込むよう、--data-dir を引き継ぐ
	_, err = fmt.Fprintf(stdout, "取り込むには: cloudlaunch-cli legacy link%s --game %s --legacy-game %s\n",
		dataDirFlag(dataDir), gameID, legacyGameID)
	return err
}

// dataDirFlag は、案内するコマンドに --data-dir を引用して引き継ぐ。落とすと、既定の DB を相手に実行されてしまう。
func dataDirFlag(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return " --data-dir " + strconv.Quote(dataDir)
}

func printLegacyGames(w io.Writer, heading string, games []legacy.Game) error {
	if len(games) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, heading); err != nil {
		return err
	}
	for _, g := range games {
		if _, err := fmt.Fprintf(w, "%q\t%q\t%d\t%d\t%s\n",
			g.ID, g.Title, g.Sessions, g.TotalSec, g.LastPlayedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}

// checkAbsPaths は、存在は確かめない。2 台目の PC では、まだセーブフォルダがないことがある（仕様書 4 章の「local なし」）。
func checkAbsPaths(saveDir, exe string) error {
	for _, f := range []struct{ name, path string }{{"--save-dir", saveDir}, {"--exe", exe}} {
		if !filepath.IsAbs(f.path) {
			return fmt.Errorf("%s must be an absolute path: %q", f.name, f.path)
		}
	}
	return nil
}

// recoveryLabel は、R2 で確かめるときに仕様書の復旧表と突き合わせられるよう、行の ID を添える。
func recoveryLabel(rc syncore.RecoveryCase) string {
	switch rc {
	case syncore.RecoveryRemoveStage:
		return "stage を削除した（6-R1）"
	case syncore.RecoveryRestoreOld:
		return "退避したセーブを戻した（6-R2）"
	case syncore.RecoveryBackupOld:
		return "退避したセーブをバックアップへ移した（6-R3）"
	case syncore.RecoveryNothing:
		return "片付けるものはなかった（6-R4）"
	}
	return fmt.Sprintf("不明な復旧（%d）", int(rc))
}

// explainSyncError は、呼び出し側がすることの決まっているエラーに、利用者向けの説明を添える。
func explainSyncError(err error) error {
	switch {
	case errors.Is(err, syncore.ErrPlanStale):
		return fmt.Errorf("状況が変わりました。もう一度実行してください: %w", err)
	case errors.Is(err, syncore.ErrSyncInProgress):
		return fmt.Errorf("別の同期が実行中です: %w", err)
	case errors.Is(err, syncore.ErrGameRunning):
		return fmt.Errorf("ゲームが実行中です。終了してからもう一度実行してください: %w", err)
	case errors.Is(err, syncore.ErrGameExeNotFound):
		return fmt.Errorf("登録した実行ファイルが見つかりません。ゲームの実行ファイルのパスを直してください: %w", err)
	case errors.Is(err, syncore.ErrLocalMissing):
		return fmt.Errorf("ローカルにセーブがないので、アップロードで上書きできません。セーブフォルダの設定を確かめてください: %w", err)
	case errors.Is(err, syncore.ErrRemoteMissing):
		return fmt.Errorf("リモートにセーブがないので、ダウンロードで上書きできません: %w", err)
	}
	return err
}

// localConfig は、recover と、リモートを使うコマンドで、バックアップの置き場所と同期ロックを揃えるために 1 か所で組み立てる。
func localConfig(s *store.Store, dataDir string) syncore.Config {
	return syncore.Config{
		Local:      s,
		Logger:     newLogger(),
		BackupsDir: filepath.Join(dataDir, "backups"),
		Locker:     platform.NewFileLock(filepath.Join(dataDir, "sync.lock")),
		Rename:     platform.Rename,
	}
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
	if err := checkAbsPaths(*saveDir, *exe); err != nil {
		return err
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
	if _, err := fmt.Fprintln(stdout, gameID); err != nil {
		return err
	}
	return printLegacyMatch(ctx, s, gameID, *title, *dataDir, stdout)
}

// gameSetImage は、画像をキャッシュに入れて DB だけを書き換え、リモートには次の push・pull で画像 → game.json の順に
// 反映する（仕様書 8-I1）。--clear は画像を外す。
func gameSetImage(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("game set-image", flag.ContinueOnError)
	game := fs.String("game", "", "ゲーム ID")
	file := fs.String("file", "", "画像のファイル（PNG・JPEG・WebP、10 MiB まで）")
	clearImage := fs.Bool("clear", false, "画像を外す")
	dataDir := fs.String("data-dir", "", "DB などを置くディレクトリ")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *game == "" {
		return errors.New("--game is required")
	}
	if (*file == "") == !*clearImage {
		return errors.New("specify either --file or --clear")
	}

	s, dir, err := openStore(ctx, *dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	// 登録していないゲームの ID を打ち間違えたときに、どこからも参照されない画像をキャッシュに残さない
	if _, err := s.GameInfo(ctx, *game); err != nil {
		return err
	}
	var sha string
	if *file != "" {
		f, err := os.Open(*file)
		if err != nil {
			return fmt.Errorf("open image: %w", err)
		}
		defer func() { _ = f.Close() }()
		// CacheImage はリモートに触れないので、接続先を読まずに Core を作る
		sha, err = metasync.New(metasync.Config{ImageDir: imageDir(dir)}).CacheImage(f)
		if err != nil {
			return err
		}
	}
	if err := s.EditGame(ctx, *game, store.GameEdit{ImageSha256: &sha}, time.Now()); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "imageSha256: %s\n", orDash(sha))
	return err
}

func imageDir(dataDir string) string {
	return filepath.Join(dataDir, "images")
}

// gameEdit は DB にだけ書き、リモートには次の push・pull で反映する（仕様書 8-M2）。指定した flag の項目だけを変えるので、
// --brand "" で brand を消せる。
func gameEdit(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("game edit", flag.ContinueOnError)
	game := fs.String("game", "", "ゲーム ID")
	title := fs.String("title", "", "ゲームのタイトル")
	brand := fs.String("brand", "", "ブランド名。空文字で消す")
	dataDir := fs.String("data-dir", "", "DB などを置くディレクトリ")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *game == "" {
		return errors.New("--game is required")
	}
	var edit store.GameEdit
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "title":
			edit.Title = title
		case "brand":
			edit.Brand = brand
		}
	})
	if edit.Title == nil && edit.Brand == nil {
		return errors.New("specify --title or --brand")
	}

	s, _, err := openStore(ctx, *dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	if err := s.EditGame(ctx, *game, edit, time.Now()); err != nil {
		return err
	}
	g, err := s.GameInfo(ctx, *game)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "title: %s\nbrand: %s\nupdatedAt: %s\n", g.Title, orDash(g.Brand), g.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

// sessionAdd は DB にだけ書き、リモートには次の push・pull でアップロードする（仕様書 8-M2）。
func sessionAdd(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("session add", flag.ContinueOnError)
	game := fs.String("game", "", "ゲーム ID")
	duration := fs.Int64("duration", -1, "プレイ時間（秒）")
	playedAt := fs.String("played-at", "", "プレイした日時（RFC3339）。省くと今の時刻")
	dataDir := fs.String("data-dir", "", "DB などを置くディレクトリ")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *game == "" {
		return errors.New("--game is required")
	}
	if *duration < 0 {
		return errors.New("--duration is required and must be >= 0")
	}
	played := time.Now()
	if *playedAt != "" {
		t, err := time.Parse(time.RFC3339Nano, *playedAt)
		if err != nil {
			return fmt.Errorf("--played-at must be RFC3339: %w", err)
		}
		played = t
	}
	u, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate session id: %w", err)
	}

	s, _, err := openStore(ctx, *dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	if err := s.CreateSession(ctx, metasync.Session{
		ID:          u.String(),
		GameID:      *game,
		PlayedAt:    played,
		DurationSec: *duration,
	}); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, u.String())
	return err
}

func legacyImport(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("legacy import", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "DB などを置くディレクトリ")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case fs.NArg() == 0:
		return errors.New("export file is required")
	case fs.NArg() > 1:
		// flag は最初の位置引数で読むのをやめるので、ファイルの後ろに書いたフラグもここに来る
		return fmt.Errorf("only one export file is allowed, and flags must come before it: %q", fs.Args())
	}

	f, err := os.Open(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("open export: %w", err)
	}
	defer func() { _ = f.Close() }()

	s, _, err := openStore(ctx, *dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	res, err := legacy.Import(ctx, f, s)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "旧版のプレイ履歴を %d 件保持しました（既にある %d 件、games にない %d 件は保持していません）\n",
		res.Added, res.Existing, res.Orphaned)
	return err
}

// legacyLink は、game add・game import が出した提案や候補を見て実行することを、仕様書 9-K1 の確認とみなす。
func legacyLink(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("legacy link", flag.ContinueOnError)
	game := fs.String("game", "", "取り込み先のゲームの ID")
	legacyGame := fs.String("legacy-game", "", "取り込む旧版のゲームの ID")
	dataDir := fs.String("data-dir", "", "DB などを置くディレクトリ")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *game == "" || *legacyGame == "" {
		return errors.New("--game and --legacy-game are required")
	}

	s, _, err := openStore(ctx, *dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	res, err := legacy.Link(ctx, s, *legacyGame, *game)
	if err != nil {
		return err
	}
	if res == (legacy.LinkResult{}) {
		// 取り込み済みのときも、--legacy-game を打ち間違えたときも 0 件になる。取り込めたと誤解されないよう分けて出す
		_, err = fmt.Fprintf(stdout, "旧版の gameId %q の未紐付けの旧履歴はありません（取り込み済みか、ID が違います）\n", *legacyGame)
		return err
	}
	_, err = fmt.Fprintf(stdout, "旧版のプレイ履歴を %d 件取り込みました（既にある %d 件は取り込まずに旧履歴から消しました）\n",
		res.Linked, res.Dropped)
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

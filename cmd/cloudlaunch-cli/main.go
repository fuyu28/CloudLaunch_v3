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

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/objstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/platform"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

const usage = `usage:
  cloudlaunch-cli game add --title <タイトル> --save-dir <パス> --exe <パス> [--brand <ブランド>] [--id <gameId>] [--data-dir <dir>]
  cloudlaunch-cli session add --game <gameId> --duration <秒> [--played-at <RFC3339>] [--data-dir <dir>]
  cloudlaunch-cli status --game <gameId> [--data-dir <dir>]
  cloudlaunch-cli push --game <gameId> [--overwrite] [--data-dir <dir>]
  cloudlaunch-cli pull --game <gameId> [--overwrite] [--data-dir <dir>]
  cloudlaunch-cli recover [--data-dir <dir>]

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
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "game":
		if len(args) >= 2 && args[1] == "add" {
			return gameAdd(ctx, args[2:], stdout)
		}
	case "session":
		if len(args) >= 2 && args[1] == "add" {
			return sessionAdd(ctx, args[2:], stdout)
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
	if err := printPlan(stdout, plan); err != nil {
		return err
	}
	return recordBaseIfNeeded(ctx, c.sync, plan, stdout)
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
// --overwrite を付けると、判定によらず上書きする。セーブの同期が見送り・失敗で終わっても、そのあとにメタ情報を同期する
// （仕様書 8-M2）。メタ情報の同期の失敗は、返すエラーに含めない（8-M3）。
func transfer(ctx context.Context, args []string, stdout io.Writer, cmd transferCommand) error {
	var overwrite bool
	c, err := openCore(ctx, cmd.name, args, &overwrite)
	if err != nil {
		return err
	}
	defer c.close()

	err = syncSave(ctx, c.sync, c.gameID, stdout, cmd, overwrite)
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
	_, err = fmt.Fprintf(stdout, "プレイ履歴を %d 件アップロードし、%d 件取り込みました\n", res.UploadedSessions, res.DownloadedSessions)
	return err
}

func syncSave(ctx context.Context, core *syncore.Core, gameID string, stdout io.Writer, cmd transferCommand, overwrite bool) error {
	plan, err := core.Status(ctx, gameID)
	if err != nil {
		return err
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
	sync   *syncore.Core
	meta   *metasync.Core
	logger *slog.Logger
	gameID string
	close  func()
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

	remote, err := objstore.New(objstore.Config{
		Endpoint:        os.Getenv("CLOUDLAUNCH_S3_ENDPOINT"),
		Region:          os.Getenv("CLOUDLAUNCH_S3_REGION"),
		Bucket:          os.Getenv("CLOUDLAUNCH_S3_BUCKET"),
		AccessKeyID:     os.Getenv("CLOUDLAUNCH_S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("CLOUDLAUNCH_S3_SECRET_ACCESS_KEY"),
	})
	if err != nil {
		return cores{}, err
	}
	// deviceName は DB の設定に置き、初期値をホスト名にする（仕様書 3 章）。変更する手段ができるまでは初期値をそのまま渡す
	host, err := os.Hostname()
	if err != nil {
		return cores{}, fmt.Errorf("hostname: %w", err)
	}
	s, dir, err := openStore(ctx, *dataDir)
	if err != nil {
		return cores{}, err
	}

	cfg := localConfig(s, dir)
	cfg.Remote = remote
	cfg.DeviceName = host
	cfg.Processes = platform.Processes{}
	return cores{
		sync:   syncore.New(cfg),
		meta:   metasync.New(metasync.Config{Remote: remote, Local: s, Logger: cfg.Logger}),
		logger: cfg.Logger,
		gameID: *game,
		close:  func() { _ = s.Close() },
	}, nil
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
		Logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
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

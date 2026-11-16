// Package metasync は、ゲーム情報とプレイ履歴（メタ情報）を、セーブの判定とは別に確認なしで同期する（仕様書 8 章）。
package metasync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

type ObjectStore interface {
	// Get はキーがなければ fs.ErrNotExist で包んだエラーを返す
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Put(ctx context.Context, key string, body io.ReadSeeker, size int64) error
	List(ctx context.Context, prefix string) ([]string, error)
	// DeleteMany は、キーがなくてもエラーにしない
	DeleteMany(ctx context.Context, keys []string) error
	// ListPrefixes は、prefix の直下にある共通の接頭辞（`/` までを含む）を返す
	ListPrefixes(ctx context.Context, prefix string) ([]string, error)
}

type Session struct {
	ID          string
	GameID      string
	PlayedAt    time.Time
	DurationSec int64
}

type LocalStore interface {
	Sessions(ctx context.Context, gameID string) ([]Session, error)
	// IsRemoteDeleted は、ゲームの tombstone の記録（仕様書 8-T2）があれば true を返す。登録していないゲームはエラーにする
	IsRemoteDeleted(ctx context.Context, gameID string) (bool, error)
	// ImportSessions は、sessions を 1 つのトランザクションで gameID のセッションとして入れる。同じゲームに同じ ID が
	// あれば飛ばし、別のゲームに同じ ID があれば入れずに ImportResult.OtherGame で返す（仕様書 8-S5）
	ImportSessions(ctx context.Context, gameID string, sessions []Session) (ImportResult, error)
	GameInfo(ctx context.Context, gameID string) (GameInfo, error)
	// ReplaceGameInfo は、DB の updatedAt が g.UpdatedAt より小さいときだけ、g の全項目で書き換えて true を返す（仕様書 8-G3）
	ReplaceGameInfo(ctx context.Context, g GameInfo) (bool, error)
	// GameIDs は、この PC に登録したすべてのゲームの ID を返す。ゲームの tombstone の記録があるゲームも含む
	GameIDs(ctx context.Context) ([]string, error)
	// RegisterGame は、g の全項目（createdAt・updatedAt を含む）をそのまま入れ、base なしで登録する（仕様書 8-N3・8-N4）。
	// 登録済みの ID ならエラーにする
	RegisterGame(ctx context.Context, g GameInfo, saveFolderPath, exePath string) error
	// ReuploadGame は、1 つのトランザクションで、g で新しいゲーム情報を作り、oldID のプレイ履歴とローカル情報（base と
	// ゲームの tombstone の記録を除く）を g.ID に移し、oldID のゲーム情報を消す（仕様書 8-U4）。oldID にゲームの tombstone の
	// 記録がなければ、何も変えずにエラーを返す
	ReuploadGame(ctx context.Context, oldID string, g GameInfo) error
	// SetImageSha256 は、sha が空なら画像を外す。updatedAt を now 以降に進め、次の同期で game.json を PUT させる（仕様書 8-G2）
	SetImageSha256(ctx context.Context, gameID, sha string, now time.Time) error
}

// GameInfo は game.json と同じ項目（仕様書 8 章）。Brand と ImageSha256 は空文字を未設定とする。
type GameInfo struct {
	ID          string
	Title       string
	Brand       string
	ImageSha256 string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type GameInfoOutcome int

const (
	GameInfoUnchanged GameInfoOutcome = iota
	GameInfoUploaded
	GameInfoDownloaded
	// GameInfoInvalid は、リモートの game.json が不正（仕様書 8-V7〜8-V11・8-V15）で同期しなかったことを表す
	GameInfoInvalid
)

type ImageOutcome int

const (
	// ImageNone は、画像を扱わなかったこと（画像がない・同期を飛ばした・game.json が不正）を表す
	ImageNone ImageOutcome = iota
	// ImageUnchanged は、画像がキャッシュにあり、転送しなかったことを表す。リモートにあるかは、game.json を PUT するときだけ
	// 確かめる
	ImageUnchanged
	ImageUploaded
	ImageDownloaded
	// ImageInvalid は、取得した画像が不正（仕様書 8-V12〜8-V14）で、キャッシュに入れなかったことを表す
	ImageInvalid
	// ImageMissing は、要る画像がリモートになく、PUT できるものもキャッシュになかったことを表す。game.json は、
	// imageSha256 を入れたまま PUT する（仕様書 8-I4）
	ImageMissing
)

type ImportResult struct {
	Inserted  int
	OtherGame []string
}

// ErrInvalidTombstone は、TombstoneReader が、ゲームの tombstone が不正（仕様書 8-T4〜8-T6）なことを表すために包んで返す。
var ErrInvalidTombstone = errors.New("invalid game tombstone")

// TombstoneReader は、ゲームの tombstone をリモートから読む。tombstone の検証と扱いは syncore が持つので、metasync は結果だけを
// 受け取る。
type TombstoneReader interface {
	// RemoteTombstoneExists は、DB の記録を見ずに deleted.json だけを読み、なければ false を返す。不正なら
	// ErrInvalidTombstone で包んだエラーを、読めなければそれ以外のエラーを返す
	RemoteTombstoneExists(ctx context.Context, gameID string) (bool, error)
}

type Config struct {
	Remote ObjectStore
	Local  LocalStore
	// Tombstones は、取り込み（RemoteGames・ImportGame）が使う。nil なら、取り込みはリモートを読まずにエラーにする
	Tombstones TombstoneReader
	// Logger は、nil ならログを捨てる
	Logger *slog.Logger
	// ImageDir は、画像のキャッシュのフォルダ。なければ作る
	ImageDir string
}

type Core struct {
	cfg Config
}

func New(cfg Config) *Core {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Core{cfg: cfg}
}

type Result struct {
	GameInfo           GameInfoOutcome
	Image              ImageOutcome
	UploadedSessions   int
	DownloadedSessions int
}

// Sync は、1 つのゲームのメタ情報を、ゲーム情報 → プレイ履歴の順に同期する。同期ロックは握らない（仕様書 3 章「排他」）。
// ゲーム情報の同期が失敗したら、プレイ履歴は同期せずにエラーを返す。不正な game.json は失敗に数えない。
// ゲームの tombstone の記録があれば、リモートに触れずにゼロ値を返す（仕様書 8-M4）。
func (c *Core) Sync(ctx context.Context, gameID string) (Result, error) {
	deleted, err := c.cfg.Local.IsRemoteDeleted(ctx, gameID)
	if err != nil {
		return Result{}, fmt.Errorf("read remote deleted: %w", err)
	}
	if deleted {
		return Result{}, nil
	}
	info, img, err := c.syncGameInfo(ctx, gameID)
	if err != nil {
		return Result{}, err
	}
	res, err := c.syncSessions(ctx, gameID)
	if err != nil {
		return Result{}, err
	}
	res.GameInfo, res.Image = info, img
	return res, nil
}

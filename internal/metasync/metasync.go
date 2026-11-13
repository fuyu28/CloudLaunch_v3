// Package metasync は、ゲーム情報とプレイ履歴（メタ情報）を、セーブの判定とは別に確認なしで同期する（仕様書 8 章）。
package metasync

import (
	"context"
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
}

type Session struct {
	ID          string
	GameID      string
	PlayedAt    time.Time
	DurationSec int64
}

type LocalStore interface {
	Sessions(ctx context.Context, gameID string) ([]Session, error)
	// RemoteDeleted は、ゲームの tombstone の記録（仕様書 8-T2）があれば true を返す。登録していないゲームはエラーにする
	RemoteDeleted(ctx context.Context, gameID string) (bool, error)
	// ImportSessions は、sessions を 1 つのトランザクションで gameID のセッションとして入れる。同じゲームに同じ ID が
	// あれば飛ばし、別のゲームに同じ ID があれば入れずに ImportResult.OtherGame で返す（仕様書 8-S5）
	ImportSessions(ctx context.Context, gameID string, sessions []Session) (ImportResult, error)
	GameInfo(ctx context.Context, gameID string) (GameInfo, error)
	// ReplaceGameInfo は、DB の updatedAt が g.UpdatedAt より小さいときだけ、g の全項目で書き換えて true を返す（仕様書 8-G3）
	ReplaceGameInfo(ctx context.Context, g GameInfo) (bool, error)
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
	// GameInfoInvalid は、リモートの game.json が不正（仕様書 8-V7〜8-V11）で同期しなかったことを表す
	GameInfoInvalid
)

type ImportResult struct {
	Inserted  int
	OtherGame []string
}

type Config struct {
	Remote ObjectStore
	Local  LocalStore
	Logger *slog.Logger
}

type Core struct {
	cfg Config
}

func New(cfg Config) *Core {
	return &Core{cfg: cfg}
}

type Result struct {
	GameInfo           GameInfoOutcome
	UploadedSessions   int
	DownloadedSessions int
}

// Sync は、1 つのゲームのメタ情報を、ゲーム情報 → プレイ履歴の順に同期する。同期ロックは握らない（仕様書 3 章「排他」）。
// ゲーム情報の同期が失敗したら、プレイ履歴は同期せずにエラーを返す。不正な game.json は失敗に数えない。
// ゲームの tombstone の記録があれば、リモートに触れずにゼロ値を返す（仕様書 8-M4）。
func (c *Core) Sync(ctx context.Context, gameID string) (Result, error) {
	deleted, err := c.cfg.Local.RemoteDeleted(ctx, gameID)
	if err != nil {
		return Result{}, fmt.Errorf("read remote deleted: %w", err)
	}
	if deleted {
		return Result{}, nil
	}
	info, err := c.syncGameInfo(ctx, gameID)
	if err != nil {
		return Result{}, err
	}
	res, err := c.syncSessions(ctx, gameID)
	if err != nil {
		return Result{}, err
	}
	res.GameInfo = info
	return res, nil
}

// Package metasync は、ゲーム情報とプレイ履歴（メタ情報）を、セーブの判定とは別に確認なしで同期する（仕様書 8 章）。
package metasync

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"
)

type ObjectStore interface {
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
	UploadedSessions int
}

// Sync は、1 つのゲームのメタ情報を同期する。同期ロックは握らない（仕様書 3 章「排他」）。
func (c *Core) Sync(ctx context.Context, gameID string) (Result, error) {
	return Result{}, errors.New("not implemented")
}

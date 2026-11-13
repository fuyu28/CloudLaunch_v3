// Package legacy は、旧版（CloudLaunch_Go）のエクスポートからプレイ履歴を取り込む（仕様書 9 章）。
package legacy

import (
	"context"
	"errors"
	"io"
	"time"
)

// Session は未紐付けの旧履歴の 1 件。GameID と GameTitle は旧版のゲームのもの。
type Session struct {
	ID          string
	GameID      string
	GameTitle   string
	PlayedAt    time.Time
	DurationSec int64
}

type Store interface {
	// AddLegacySessions は、1 つのトランザクションで未紐付けの旧履歴に入れ、入れた件数を返す。未紐付けの旧履歴か
	// 通常のプレイ履歴に同じ ID があるものは入れない（仕様書 9-L3）
	AddLegacySessions(ctx context.Context, sessions []Session) (int, error)
}

type Result struct {
	Added int
	// Existing は、未紐付けの旧履歴か通常のプレイ履歴に同じ ID があって保持しなかった件数
	Existing int
	// Orphaned は、games にない gameId を持つので保持しなかった件数（仕様書 9-L2）
	Orphaned int
}

func Import(_ context.Context, _ io.Reader, _ Store) (Result, error) {
	return Result{}, errors.New("not implemented")
}

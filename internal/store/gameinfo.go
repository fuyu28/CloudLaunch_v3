// ゲーム情報（games）の読み込み、メタ情報の同期による置き換え（仕様書 8 章「ゲーム情報」）、CLI からの編集を行う。
package store

import (
	"context"
	"time"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
)

// GameEdit は、nil の項目を変えない。Brand の空文字は brand を消す。
type GameEdit struct {
	Title *string
	Brand *string
}

func (s *Store) GameInfo(ctx context.Context, gameID string) (metasync.GameInfo, error) {
	return metasync.GameInfo{}, nil
}

func (s *Store) ReplaceGameInfo(ctx context.Context, g metasync.GameInfo) (bool, error) {
	return false, nil
}

func (s *Store) EditGame(ctx context.Context, gameID string, edit GameEdit, now time.Time) error {
	return nil
}

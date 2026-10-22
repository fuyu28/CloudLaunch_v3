// ゲームの登録（games と game_locals への書き込み）を行う。
package store

import (
	"context"
	"errors"
	"time"
)

// NewGame は登録するゲームの値。ID と日時は呼び出し側が決める。
type NewGame struct {
	ID             string
	Title          string
	Brand          string // 空文字なら未設定として NULL を書く
	SaveFolderPath string
	ExePath        string
	CreatedAt      time.Time
}

// CreateGame は games と game_locals に 1 行ずつ書き込む。
func (s *Store) CreateGame(ctx context.Context, g NewGame) error {
	return errors.New("not implemented")
}

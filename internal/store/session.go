// プレイ履歴（play_sessions）の読み書きと、メタ情報の同期が使うゲームのローカル情報の読み込みを行う。
package store

import (
	"context"
	"errors"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
)

func (s *Store) CreateSession(ctx context.Context, sess metasync.Session) error {
	return errors.New("not implemented")
}

func (s *Store) Sessions(ctx context.Context, gameID string) ([]metasync.Session, error) {
	return nil, errors.New("not implemented")
}

func (s *Store) RemoteDeleted(ctx context.Context, gameID string) (bool, error) {
	return false, errors.New("not implemented")
}

// Pull ジャーナル（仕様書 6 章）の読み書きを行う。
package store

import (
	"context"
	"errors"
	"time"
)

func (s *Store) InsertPullOperation(ctx context.Context, opID, gameID, livePath, stagePath, oldPath string, createdAt time.Time) error {
	return errors.New("not implemented")
}

func (s *Store) DeletePullOperation(ctx context.Context, opID string) error {
	return errors.New("not implemented")
}

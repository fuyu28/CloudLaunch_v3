// 旧版から読み込んだ未紐付けの旧履歴（legacy_sessions）の読み書きを行う（仕様書 9 章）。
package store

import (
	"context"
	"errors"

	"github.com/fuyu28/CloudLaunch_v3/internal/legacy"
)

func (s *Store) AddLegacySessions(_ context.Context, _ []legacy.Session) (int, error) {
	return 0, errors.New("not implemented")
}

func (s *Store) LegacySessions(_ context.Context) ([]legacy.Session, error) {
	return nil, errors.New("not implemented")
}

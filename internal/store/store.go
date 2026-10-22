// Package store はローカルの SQLite を開き、migration を適用し、同期コアが使う状態を読み書きする（仕様書 3 章）。
// テーブルの定義は migrations/ の SQL を正とする。
package store

import (
	"context"
	"database/sql"
	"errors"
)

// Store はローカルの SQLite への接続を持つ。
type Store struct {
	db *sql.DB
}

// Open は path の DB を開き、未適用の migration を最後まで適用する。DB のファイルがなければ作る。
func Open(ctx context.Context, path string) (*Store, error) {
	return nil, errors.New("not implemented")
}

// Close は DB を閉じる。
func (s *Store) Close() error {
	return errors.New("not implemented")
}

// Package store はローカルの SQLite を開き、migration を適用し、同期コアが使う状態を読み書きする（仕様書 3 章）。
// テーブルの定義は migrations/ の SQL を正とする。
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// PRAGMA は実行した接続にしか効かず、database/sql は接続を作り直すので、Open 後に一度 Exec するのではなく DSN で全接続に適用する
const dsnParams = "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

// timeLayout は仕様書 3 章の日時の形式（RFC3339 の UTC・ミリ秒固定長）。
const timeLayout = "2006-01-02T15:04:05.000Z"

// Store はローカルの SQLite への接続を持つ。
type Store struct {
	db *sql.DB
}

// Open は path の DB を開き、未適用の migration を最後まで適用する。DB のファイルがなければ作る。
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+dsnParams)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	dir, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("migrations dir: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, dir)
	if err != nil {
		return fmt.Errorf("migration provider: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// Close は DB を閉じる。
func (s *Store) Close() error {
	return s.db.Close()
}

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

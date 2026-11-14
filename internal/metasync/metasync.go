// Package metasync は、ゲーム情報とプレイ履歴（メタ情報）を、セーブの判定とは別に確認なしで同期する（仕様書 8 章）。
package metasync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

// parseTimestamp は、3 章の日時の形の JSON 文字列だけを受け入れる。ない項目・null・文字列でない値は通さない。
// time.Parse は表記の揺れ（ミリ秒の桁数など）を受け入れうるので、書き戻して元の表記と一致するものだけを通す。
func parseTimestamp(raw json.RawMessage) (time.Time, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(timeLayout, s)
	if err != nil || t.UTC().Format(timeLayout) != s {
		return time.Time{}, false
	}
	return t, true
}

type ObjectStore interface {
	// Get はキーがなければ fs.ErrNotExist で包んだエラーを返す
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Put(ctx context.Context, key string, body io.ReadSeeker, size int64) error
	List(ctx context.Context, prefix string) ([]string, error)
	// DeleteMany は、キーがなくてもエラーにしない
	DeleteMany(ctx context.Context, keys []string) error
	// ListPrefixes は、prefix の直下にある共通の接頭辞（`/` までを含む）を返す
	ListPrefixes(ctx context.Context, prefix string) ([]string, error)
}

type Session struct {
	ID          string
	GameID      string
	PlayedAt    time.Time
	DurationSec int64
}

type LocalStore interface {
	Sessions(ctx context.Context, gameID string) ([]Session, error)
	// RemoteDeleted は、ゲームの tombstone の記録（仕様書 8-T2）があれば、その値と true を返す。登録していないゲームは
	// エラーにする
	RemoteDeleted(ctx context.Context, gameID string) (Tombstone, bool, error)
	// RecordRemoteDeleted は、ゲームの tombstone の記録を書く。登録していないゲームはエラーにする
	RecordRemoteDeleted(ctx context.Context, gameID string, t Tombstone) error
	// ImportSessions は、sessions を 1 つのトランザクションで gameID のセッションとして入れる。同じゲームに同じ ID が
	// あれば飛ばし、別のゲームに同じ ID があれば入れずに ImportResult.OtherGame で返す（仕様書 8-S5）
	ImportSessions(ctx context.Context, gameID string, sessions []Session) (ImportResult, error)
	GameInfo(ctx context.Context, gameID string) (GameInfo, error)
	// ReplaceGameInfo は、DB の updatedAt が g.UpdatedAt より小さいときだけ、g の全項目で書き換えて true を返す（仕様書 8-G3）
	ReplaceGameInfo(ctx context.Context, g GameInfo) (bool, error)
	// GameIDs は、この PC に登録したすべてのゲームの ID を返す。ゲームの tombstone の記録があるゲームも含む
	GameIDs(ctx context.Context) ([]string, error)
	// RegisterGame は、g の全項目（createdAt・updatedAt を含む）をそのまま入れ、base なしで登録する（仕様書 8-N3・8-N4）。
	// 登録済みの ID ならエラーにする
	RegisterGame(ctx context.Context, g GameInfo, saveFolderPath, exePath string) error
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
	// GameInfoInvalid は、リモートの game.json が不正（仕様書 8-V7〜8-V11・8-V15）で同期しなかったことを表す
	GameInfoInvalid
)

type ImageOutcome int

const (
	// ImageNone は、画像を扱わなかったこと（画像がない・同期を飛ばした・game.json が不正）を表す
	ImageNone ImageOutcome = iota
	// ImageUnchanged は、画像がキャッシュにあり、転送しなかったことを表す。リモートにあるかは、game.json を PUT するときだけ
	// 確かめる
	ImageUnchanged
	ImageUploaded
	ImageDownloaded
	// ImageInvalid は、取得した画像が不正（仕様書 8-V12〜8-V14）で、キャッシュに入れなかったことを表す
	ImageInvalid
	// ImageMissing は、要る画像がリモートになく、PUT できるものもキャッシュになかったことを表す。game.json は、
	// imageSha256 を入れたまま PUT する（仕様書 8-I4）
	ImageMissing
)

type ImportResult struct {
	Inserted  int
	OtherGame []string
}

type Config struct {
	Remote ObjectStore
	Local  LocalStore
	Logger *slog.Logger
	// ImageDir は、画像のキャッシュのフォルダ。なければ作る
	ImageDir string
}

type Core struct {
	cfg Config
}

func New(cfg Config) *Core {
	return &Core{cfg: cfg}
}

type Result struct {
	GameInfo           GameInfoOutcome
	Image              ImageOutcome
	UploadedSessions   int
	DownloadedSessions int
}

// Sync は、1 つのゲームのメタ情報を、ゲーム情報 → プレイ履歴の順に同期する。同期ロックは握らない（仕様書 3 章「排他」）。
// ゲーム情報の同期が失敗したら、プレイ履歴は同期せずにエラーを返す。不正な game.json は失敗に数えない。
// ゲームの tombstone の記録があれば、リモートに触れずにゼロ値を返す（仕様書 8-M4）。
func (c *Core) Sync(ctx context.Context, gameID string) (Result, error) {
	_, deleted, err := c.cfg.Local.RemoteDeleted(ctx, gameID)
	if err != nil {
		return Result{}, fmt.Errorf("read remote deleted: %w", err)
	}
	if deleted {
		return Result{}, nil
	}
	info, img, err := c.syncGameInfo(ctx, gameID)
	if err != nil {
		return Result{}, err
	}
	res, err := c.syncSessions(ctx, gameID)
	if err != nil {
		return Result{}, err
	}
	res.GameInfo, res.Image = info, img
	return res, nil
}

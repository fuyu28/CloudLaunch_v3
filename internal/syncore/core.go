// コアの設定と、コアが使う外部（リモート・DB）のインターフェース。各操作は、仕様書の章ごとのファイルにある。
package syncore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
)

// transferLimit は、Push・Pull・GC がリモートと並列にやりとりする数。
const transferLimit = 8

var (
	errBackupsDirNotSet = errors.New("backups directory is not configured")
	errRenameNotSet     = errors.New("rename is not configured")
)

type ObjectStore interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Put(ctx context.Context, key string, body io.ReadSeeker, size int64) error
	List(ctx context.Context, prefix string) ([]string, error)
	// DeleteMany は、キーがなくてもエラーにしない
	DeleteMany(ctx context.Context, keys []string) error
}

// GameLocal は、ゲームのローカル情報（同期しない）のうち、同期コアが使うもの。BaseManifestHash は未設定なら空。
type GameLocal struct {
	SaveFolderPath   string
	ExePath          string
	BaseManifestHash string
}

type LocalStore interface {
	GameLocal(ctx context.Context, gameID string) (GameLocal, error)
	SetBase(ctx context.Context, gameID, manifestHash string) error
	InsertPullOperation(ctx context.Context, opID, gameID, livePath, stagePath, oldPath string, createdAt time.Time) error
	DeletePullOperation(ctx context.Context, opID string) error
	ListPullOperations(ctx context.Context) ([]PullOperation, error)
}

type Config struct {
	Remote     ObjectStore
	Local      LocalStore
	Logger     *slog.Logger
	DeviceName string
	// BackupsDir は、Pull で退避したセーブを置くディレクトリ。その下の {gameId}/ に置く
	BackupsDir string
	// Locker は、Push・Pull・Recover が実行中に握る PC 全体の同期ロック（仕様書 3 章）
	Locker Locker
	// Processes は、Push・Pull がゲームの実行中を拒否するために使う（仕様書 7 章）
	Processes ProcessChecker
	// Rename は、リネームをディスクへ書き出してから戻り、移す先があれば失敗する（仕様書 3 章「ディスクへの書き出し」）。
	// os.Rename はどちらも満たさないので既定にせず、nil なら Pull と Recover は何も変える前にエラーにする
	Rename func(oldpath, newpath string) error
	// Now・NewGeneration・SyncFile は、nil なら実際の時刻・UUIDv7 の採番・(*os.File).Sync を使う
	Now           func() time.Time
	NewGeneration func() (string, error)
	SyncFile      func(f *os.File) error
}

type Core struct {
	cfg Config
}

func New(cfg Config) *Core {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.SyncFile == nil {
		cfg.SyncFile = (*os.File).Sync
	}
	if cfg.NewGeneration == nil {
		cfg.NewGeneration = func() (string, error) {
			u, err := uuid.NewV7()
			if err != nil {
				return "", fmt.Errorf("generate generation id: %w", err)
			}
			return u.String(), nil
		}
	}
	return &Core{cfg: cfg}
}

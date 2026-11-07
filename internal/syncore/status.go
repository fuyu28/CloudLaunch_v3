// 状態判定（仕様書 4 章）。
package syncore

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
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
	HashCache(ctx context.Context, gameID string) ([]HashCacheEntry, error)
	ReplaceHashCache(ctx context.Context, gameID string, entries []HashCacheEntry) error
	DeleteHashCache(ctx context.Context, gameID string) error
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
	remote        ObjectStore
	local         LocalStore
	logger        *slog.Logger
	deviceName    string
	backupsDir    string
	locker        Locker
	processes     ProcessChecker
	now           func() time.Time
	newGeneration func() (string, error)
	rename        func(oldpath, newpath string) error
	syncFile      func(f *os.File) error
}

func New(cfg Config) *Core {
	c := &Core{
		remote:        cfg.Remote,
		local:         cfg.Local,
		logger:        cfg.Logger,
		deviceName:    cfg.DeviceName,
		backupsDir:    cfg.BackupsDir,
		locker:        cfg.Locker,
		processes:     cfg.Processes,
		now:           cfg.Now,
		newGeneration: cfg.NewGeneration,
		rename:        cfg.Rename,
		syncFile:      cfg.SyncFile,
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.syncFile == nil {
		c.syncFile = (*os.File).Sync
	}
	if c.newGeneration == nil {
		c.newGeneration = func() (string, error) {
			u, err := uuid.NewV7()
			if err != nil {
				return "", fmt.Errorf("generate generation id: %w", err)
			}
			return u.String(), nil
		}
	}
	return c
}

type Decision int

const (
	DecisionNoChange Decision = iota
	DecisionPush
	DecisionPull
	DecisionConflict
)

type Plan struct {
	GameID   string
	Decision Decision
	// RecordBase は、判定が「base を記録」か「base を更新」の行に当たったことを表す。どちらも local のハッシュを base に書く
	RecordBase bool
	// LocalMissing は、判定が「base あり・local なし → 競合」の行に当たったことを表す。「ローカルにセーブが見つかりません」と警告する
	LocalMissing bool
	Base         string // 空なら base なし
	Local        LocalState
	Remote       RemoteState
}

type LocalState struct {
	Entries []Entry
	Hash    string // ファイルが 1 つもなく、base が空 manifest のハッシュでなければ空
}

// ensureLocalUnchanged は、Push（5-P3）と Pull（6-L6）が、承認の後にセーブフォルダが変わっていないかを確かめる（仕様書 7 章）。
// DB の base ではなく plan.Base を使う。plan.Local.Hash は plan.Base から計算したもので、同じ base で計算し直さないと比べられない。
// Status と同じくキャッシュを使う。計算の仕方が違うと、Status は変わっていない、ここでは変わったと食い違い、
// 何度やり直しても ErrPlanStale になりうるため（仕様書 3 章）
func (c *Core) ensureLocalUnchanged(ctx context.Context, folder string, plan Plan) error {
	entries, err := c.saveFolderEntries(ctx, plan.GameID, folder)
	if err != nil {
		return err
	}
	if localHash(entries, plan.Base) != plan.Local.Hash {
		return fmt.Errorf("save folder changed after approval: %w", ErrPlanStale)
	}
	return nil
}

// localHash は、Status の判定と、Push・Pull の承認後の確認（ensureLocalUnchanged）で、同じ local を同じハッシュにするために 1 か所で決める。
// base が空 manifest なら、ファイルのない local を「なし」にしない。空にして Push した後に、毎回競合と判定しないため（仕様書 4 章）
func localHash(entries []Entry, base string) string {
	if len(entries) > 0 || base == emptyManifestHash {
		return ManifestHash(entries)
	}
	return ""
}

type RemoteState struct {
	Generation string
	Manifest   Manifest
	Hash       string // HEAD がなければ空。空 manifest の世代なら空にならない
}

// Status は同期の状態（base・リモート・セーブフォルダ・Pull ジャーナル）を変えない。ハッシュキャッシュは書く。
// キャッシュは消えても判定の結果が変わらないので、同期の状態に含めない（仕様書 3 章）。
func (c *Core) Status(ctx context.Context, gameID string) (Plan, error) {
	game, err := c.local.GameLocal(ctx, gameID)
	if err != nil {
		return Plan{}, fmt.Errorf("read game %s: %w", gameID, err)
	}
	saveFolder, base := game.SaveFolderPath, game.BaseManifestHash

	entries, err := c.saveFolderEntries(ctx, gameID, saveFolder)
	if err != nil {
		return Plan{}, err
	}
	local := LocalState{Entries: entries, Hash: localHash(entries, base)}
	remote, err := c.readRemote(ctx, gameID)
	if err != nil {
		return Plan{}, err
	}

	plan := Plan{GameID: gameID, Base: base, Local: local, Remote: remote}
	if base == "" {
		plan.Decision, plan.RecordBase = decideWithoutBase(local.Hash, remote.Hash)
	} else {
		plan.Decision, plan.RecordBase, plan.LocalMissing = decideWithBase(base, local.Hash, remote.Hash)
	}
	return plan, nil
}

// RecordBase は判定が「base を記録」か「base を更新」の plan だけを受け付ける。リモートは読み直さない。
// Status の時点で local と remote が一致していたので、その後にどちらかが変わっても、次の判定は判定表どおりに決まる。
func (c *Core) RecordBase(ctx context.Context, plan Plan) error {
	if !plan.RecordBase {
		return fmt.Errorf("record base %s: plan is not a record-base decision", plan.GameID)
	}
	if err := c.local.SetBase(ctx, plan.GameID, plan.Local.Hash); err != nil {
		return fmt.Errorf("set base: %w", err)
	}
	return nil
}

// decideWithoutBase は判定表の「base なし」の 5 行に当たる。空のハッシュは「なし」を表す。
func decideWithoutBase(localHash, remoteHash string) (Decision, bool) {
	switch {
	case localHash == "" && remoteHash == "":
		return DecisionNoChange, false
	case remoteHash == "":
		return DecisionPush, false
	case localHash == "":
		return DecisionPull, false
	case localHash == remoteHash:
		return DecisionNoChange, true
	default:
		return DecisionConflict, false
	}
}

// decideWithBase は判定表の「base あり」の 8 行に当たる。空のハッシュは「なし」を表す。
// 「なし」の行を先に調べるのは、判定表で local なし・remote なしの行が、B や変化の行より優先されるためである。
func decideWithBase(base, localHash, remoteHash string) (decision Decision, recordBase, localMissing bool) {
	switch {
	case localHash == "" && remoteHash == "":
		return DecisionNoChange, false, false
	case localHash == "":
		return DecisionConflict, false, true
	case remoteHash == "":
		return DecisionPush, false, false
	case localHash == base && remoteHash == base:
		return DecisionNoChange, false, false
	case remoteHash == base:
		return DecisionPush, false, false
	case localHash == base:
		return DecisionPull, false, false
	case localHash == remoteHash:
		return DecisionNoChange, true, false
	default:
		return DecisionConflict, false, false
	}
}

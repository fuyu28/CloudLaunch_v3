// 状態判定（仕様書 4 章）。
package syncore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

type ObjectStore interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Put(ctx context.Context, key string, body io.ReadSeeker, size int64) error
	List(ctx context.Context, prefix string) ([]string, error)
}

type LocalStore interface {
	GameLocal(ctx context.Context, gameID string) (saveFolderPath, baseManifestHash string, err error)
	SetBase(ctx context.Context, gameID, manifestHash string) error
	InsertPullOperation(ctx context.Context, opID, gameID, livePath, stagePath, oldPath string, createdAt time.Time) error
	DeletePullOperation(ctx context.Context, opID string) error
}

type Config struct {
	Remote     ObjectStore
	Local      LocalStore
	Logger     *slog.Logger
	DeviceName string
	// Now と NewGeneration は、nil なら実際の時刻と UUIDv7 の採番を使う
	Now           func() time.Time
	NewGeneration func() (string, error)
}

type Core struct {
	remote        ObjectStore
	local         LocalStore
	logger        *slog.Logger
	deviceName    string
	now           func() time.Time
	newGeneration func() (string, error)
}

func New(cfg Config) *Core {
	c := &Core{
		remote:        cfg.Remote,
		local:         cfg.Local,
		logger:        cfg.Logger,
		deviceName:    cfg.DeviceName,
		now:           cfg.Now,
		newGeneration: cfg.NewGeneration,
	}
	if c.now == nil {
		c.now = time.Now
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
	// RecordBase は、判定が「base を記録」の行に当たったことを表す
	RecordBase bool
	// LocalMissing は、判定が「base あり・local なし → 競合」の行に当たったことを表す。「ローカルにセーブが見つかりません」と警告する
	LocalMissing bool
	Base         string // 空なら base なし
	Local        LocalState
	Remote       RemoteState
}

type LocalState struct {
	Entries []Entry
	Hash    string // ファイルが 1 つもなければ空
}

type RemoteState struct {
	Generation string
	Manifest   Manifest
	Hash       string // HEAD がなければ空。空 manifest の世代なら空にならない
}

// Status はローカルにもリモートにも書き込まない。
func (c *Core) Status(ctx context.Context, gameID string) (Plan, error) {
	saveFolder, base, err := c.local.GameLocal(ctx, gameID)
	if err != nil {
		return Plan{}, fmt.Errorf("read game %s: %w", gameID, err)
	}
	if base != "" {
		return Plan{}, errors.New("status with a base is not implemented yet")
	}

	entries, err := localEntries(ctx, saveFolder)
	if err != nil {
		return Plan{}, err
	}
	local := LocalState{Entries: entries}
	if len(entries) > 0 {
		local.Hash = ManifestHash(entries)
	}
	remote, err := c.readRemote(ctx, gameID)
	if err != nil {
		return Plan{}, err
	}

	plan := Plan{GameID: gameID, Base: base, Local: local, Remote: remote}
	plan.Decision, plan.RecordBase = decideWithoutBase(local.Hash, remote.Hash)
	return plan, nil
}

// RecordBase は判定が「base を記録」の plan だけを受け付ける。リモートは読み直さない。
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

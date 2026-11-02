// 状態判定（仕様書 4 章）。
package syncore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
)

type ObjectStore interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

type LocalStore interface {
	GameLocal(ctx context.Context, gameID string) (saveFolderPath, baseManifestHash string, err error)
}

type Core struct {
	remote ObjectStore
	local  LocalStore
	logger *slog.Logger
}

func New(remote ObjectStore, local LocalStore, logger *slog.Logger) *Core {
	return &Core{remote: remote, local: local, logger: logger}
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
	Base       string // 空なら base なし
	Local      LocalState
	Remote     RemoteState
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

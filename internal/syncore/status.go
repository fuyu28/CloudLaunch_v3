// 状態判定（仕様書 4 章）。
package syncore

import (
	"context"
	"errors"
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
	Remote     *RemoteState // HEAD がなければ nil
}

type LocalState struct {
	Entries []Entry
	Hash    string // ファイルが 1 つもなければ空
}

type RemoteState struct {
	Generation string
	Manifest   Manifest
	Hash       string
}

func (c *Core) Status(ctx context.Context, gameID string) (Plan, error) {
	return Plan{}, errors.New("not implemented")
}

// 起動時の復旧（仕様書 6 章「起動時の復旧」）。
package syncore

import (
	"context"
	"errors"
	"time"
)

// PullOperation は Pull ジャーナルの 1 行。
type PullOperation struct {
	OpID      string
	GameID    string
	LivePath  string
	StagePath string
	OldPath   string
	CreatedAt time.Time
}

// RecoveryCase は、復旧した行が仕様書 6 章の復旧表のどの行に当たったかを表す。
type RecoveryCase int

const (
	RecoveryRemoveStage RecoveryCase = iota + 1 // 6-R1
	RecoveryRestoreOld                          // 6-R2
	RecoveryBackupOld                           // 6-R3
	RecoveryNothing                             // 6-R4
)

func (rc RecoveryCase) String() string {
	return ""
}

type Recovery struct {
	OpID   string
	GameID string
	Case   RecoveryCase
}

func (c *Core) Recover(ctx context.Context) ([]Recovery, error) {
	return nil, errors.New("not implemented")
}

// 状態判定（仕様書 4 章）。
package syncore

import (
	"context"
	"fmt"
)

type Decision int

const (
	DecisionNoChange Decision = iota
	DecisionPush
	DecisionPull
	DecisionConflict
)

func (d Decision) String() string {
	switch d {
	case DecisionNoChange:
		return "NoChange"
	case DecisionPush:
		return "Push"
	case DecisionPull:
		return "Pull"
	case DecisionConflict:
		return "Conflict"
	}
	return fmt.Sprintf("Decision(%d)", int(d))
}

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

// Status はローカルにもリモートにも書き込まない。
func (c *Core) Status(ctx context.Context, gameID string) (Plan, error) {
	game, err := c.local.GameLocal(ctx, gameID)
	if err != nil {
		return Plan{}, fmt.Errorf("read game %s: %w", gameID, err)
	}
	saveFolder, base := game.SaveFolderPath, game.BaseManifestHash

	entries, err := localEntries(ctx, saveFolder)
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

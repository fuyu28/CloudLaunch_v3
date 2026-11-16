// Package gamesync は、1 ゲームのセーブとメタ情報を、仕様書 8 章「セーブとメタ情報の同期」の順（8-M1〜8-M3）で同期する。
// syncore と metasync は互いに import しないので、2 つを組み合わせる手順をここに置く。
package gamesync

import (
	"context"
	"errors"
	"log/slog"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

type Config struct {
	Save *syncore.Core
	Meta *metasync.Core
	// Logger は、nil ならログを捨てる
	Logger *slog.Logger
}

type Core struct {
	cfg Config
}

func New(cfg Config) *Core {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Core{cfg: cfg}
}

// Result は、セーブの同期の後の、メタ情報の同期の結果。メタ情報の同期の失敗は、セーブの同期の結果に含めないので（仕様書 8-M3）、
// 戻り値のエラーではなく MetaErr で返す。
type Result struct {
	// RemoteDeleted は、セーブの同期の失敗の後に確かめ直して、ゲームの tombstone が見つかったときに入る。DB への記録と
	// 配下の削除（8-T2）は済んでいる
	RemoteDeleted *syncore.Tombstone
	// Meta はメタ情報の同期が成功したときに、MetaErr は失敗したときに入る。同期しなかったときは、どちらも入らない
	Meta    *metasync.Result
	MetaErr error
}

// Check は、ゲームの tombstone があれば 8-T2 の扱いにして（8-M1）、RemoteDeleted を入れた plan を返す。このとき、
// セーブもメタ情報も同期しない。配下の削除に失敗したときは、plan とともにエラーを返す。なければ判定する。
func (c *Core) Check(ctx context.Context, gameID string) (syncore.Plan, error) {
	if plan, deleted, err := c.handleTombstone(ctx, gameID); deleted || err != nil {
		return plan, err
	}
	plan, err := c.cfg.Save.Status(ctx, gameID)
	if err != nil || plan.RemoteDeleted == nil {
		return plan, err
	}
	// 確かめた後に削除された。Status は記録も削除もしないので、もう一度扱う
	plan, deleted, err := c.handleTombstone(ctx, gameID)
	if !deleted && err == nil {
		// Status が見た tombstone が見えなくなった。削除されたかどうか分からないので、判定を止める
		return syncore.Plan{GameID: gameID}, syncore.ErrRemoteDeleted
	}
	return plan, err
}

func (c *Core) handleTombstone(ctx context.Context, gameID string) (syncore.Plan, bool, error) {
	t, deleted, err := c.cfg.Save.HandleTombstone(ctx, gameID)
	if !deleted {
		return syncore.Plan{}, false, err
	}
	return syncore.Plan{GameID: gameID, RemoteDeleted: &t}, true, err
}

func (c *Core) Push(ctx context.Context, plan syncore.Plan, opts syncore.PushOptions) (Result, error) {
	return c.SyncMeta(ctx, plan.GameID, c.cfg.Save.Push(ctx, plan, opts))
}

func (c *Core) Pull(ctx context.Context, plan syncore.Plan, opts syncore.PullOptions) (Result, error) {
	return c.SyncMeta(ctx, plan.GameID, c.cfg.Save.Pull(ctx, plan, opts))
}

// Skip は、転送を見送る。plan が base の記録に当たれば記録してから（仕様書 4 章）、SyncMeta を呼ぶ。base の記録は
// データを転送しないので、承認を待たない。
func (c *Core) Skip(ctx context.Context, plan syncore.Plan) (Result, error) {
	var err error
	if plan.RecordBase {
		err = c.cfg.Save.RecordBase(ctx, plan)
	}
	return c.SyncMeta(ctx, plan.GameID, err)
}

// SyncMeta は、セーブの同期の後にメタ情報を同期する（8-M2）。saveErr はセーブの同期（Check を含む）のエラーで、返すエラーに
// 含める。saveErr があれば、同期の前にゲームの tombstone を確かめ直す。tombstone が見つかったときと、確かめ直しに失敗した
// ときは、メタ情報を同期しない。
func (c *Core) SyncMeta(ctx context.Context, gameID string, saveErr error) (Result, error) {
	if saveErr != nil {
		// 承認の後に削除されると、セーブの同期は 8-T7 のほか、削除で消えた object を取得できずにも失敗する。確かめずに
		// 同期すると、削除したデータをリモートに戻す
		plan, deleted, err := c.handleTombstone(ctx, gameID)
		if deleted || err != nil {
			return Result{RemoteDeleted: plan.RemoteDeleted}, errors.Join(saveErr, err)
		}
		// 確かめ直しで見えなくても、セーブの同期が削除済みと判断して止まったので、安全な側に倒して同期しない
		if errors.Is(saveErr, syncore.ErrRemoteDeleted) {
			return Result{}, saveErr
		}
	}
	meta, err := c.cfg.Meta.Sync(ctx, gameID)
	if err != nil {
		c.cfg.Logger.Warn("metadata sync failed", "gameId", gameID, "err", err)
		return Result{MetaErr: err}, saveErr
	}
	return Result{Meta: &meta}, saveErr
}

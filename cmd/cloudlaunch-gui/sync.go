// 1 ゲームの確認と転送（仕様書 7 章）。確認した plan は Go の側にゲームごとに持ち、Svelte にはトークンだけを渡す。
// 承認はトークンで受け付け、利用者が見ていない plan を実行しない。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/fuyu28/CloudLaunch_v3/internal/gamesync"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

const (
	progressEvent = "sync:progress"
	// progressInterval は、小さなファイルの多いセーブでイベントが詰まらないよう、進捗を送る最短の間隔
	progressInterval = 100 * time.Millisecond
)

func init() {
	application.RegisterEvent[ProgressEvent](progressEvent)
}

type ProgressEvent struct {
	GameID   string           `json:"gameId"`
	Progress syncore.Progress `json:"progress"`
}

// SyncService は Wails のサービスとして、フロントから呼ばれる。
type SyncService struct {
	save   *syncore.Core
	games  *gamesync.Core
	logger *slog.Logger
	// remoteErr は、リモートの接続情報を読めなかったときに入る。このときは確認を受け付けない
	remoteErr error
	emit      func(ProgressEvent)

	mu     sync.Mutex
	ops    map[string]*gameOp
	tokens uint64
}

// gameOp は、ゲームごとの今の操作。確認中と転送中は cancel が、確認を終えて承認を待つ間は plan が入る。
type gameOp struct {
	token        string
	cancel       context.CancelFunc
	plan         *syncore.Plan
	transferring bool
}

type CheckView struct {
	// Superseded は、同じゲームの新しい確認に置き換えられたことを表す。このときはほかの項目を使わず、表示も変えない
	Superseded bool `json:"superseded"`
	// Status は、カードに出す判定。確認に失敗したときは空
	Status string `json:"status"`
	// Error は、空でなければ失敗の文言として出す
	Error string `json:"error"`
	// Confirm は、承認を求めるときだけ入る
	Confirm *ConfirmView `json:"confirm"`
}

type ConfirmView struct {
	// Token は、承認のときに Transfer へそのまま返す
	Token        string    `json:"token"`
	Title        string    `json:"title"`
	Rows         []InfoRow `json:"rows"`
	ApproveLabel string    `json:"approveLabel"`
}

type InfoRow struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type TransferView struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

func newSyncService(save *syncore.Core, games *gamesync.Core, logger *slog.Logger, remoteErr error) *SyncService {
	return &SyncService{
		save:      save,
		games:     games,
		logger:    logger,
		remoteErr: remoteErr,
		emit: func(e ProgressEvent) {
			application.Get().Event.Emit(progressEvent, e)
		},
		ops: map[string]*gameOp{},
	}
}

// Check は、同じゲームの前の確認を打ち切ってから判定する。判定が base の記録に当たれば、データを転送しないので確認を取らずに
// 記録する（仕様書 4 章）。ゲームの tombstone があれば、gamesync が 8-T2 の扱いにする。
func (s *SyncService) Check(ctx context.Context, gameID string) CheckView {
	if s.remoteErr != nil {
		return CheckView{Error: "リモートの接続情報を読めません: " + s.remoteErr.Error()}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	op := s.beginCheck(gameID, cancel)
	if op == nil {
		return CheckView{Error: "転送中なので確認できません"}
	}

	plan, err := s.games.Check(ctx, gameID)
	if err == nil && plan.RecordBase {
		err = s.save.RecordBase(ctx, plan)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ops[gameID] != op {
		return CheckView{Superseded: true}
	}
	if err != nil {
		s.logger.Error("check failed", "gameId", gameID, "err", err)
	}
	view := checkView(plan, err)
	if view.Confirm == nil {
		delete(s.ops, gameID)
		return view
	}
	op.cancel = nil
	op.plan = &plan
	view.Confirm.Token = op.token
	return view
}

// beginCheck は、転送中のゲームには nil を返す。
func (s *SyncService) beginCheck(gameID string, cancel context.CancelFunc) *gameOp {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev := s.ops[gameID]; prev != nil {
		if prev.transferring {
			return nil
		}
		if prev.cancel != nil {
			prev.cancel()
		}
	}
	s.tokens++
	op := &gameOp{token: strconv.FormatUint(s.tokens, 10), cancel: cancel}
	s.ops[gameID] = op
	return op
}

// Transfer は、token が今の確認のものでなければ実行しない。確認し直した後に、古いダイアログから承認されたときに当たる。
// 承認の後に local・remote が変わったときは、syncore が ErrPlanStale で止める。
func (s *SyncService) Transfer(ctx context.Context, gameID, token string) TransferView {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	op := s.beginTransfer(gameID, token, cancel)
	if op == nil {
		return TransferView{Error: "確認した後に状況が変わりました。もう一度確認してください"}
	}
	defer s.finish(gameID, op)

	plan := *op.plan
	notify := throttleProgress(gameID, s.emit)
	var err error
	var done string
	switch plan.Decision {
	case syncore.DecisionPush:
		err = s.save.Push(ctx, plan, syncore.PushOptions{Progress: notify})
		done = "アップロードしました"
	case syncore.DecisionPull:
		err = s.save.Pull(ctx, plan, syncore.PullOptions{Progress: notify})
		done = "ダウンロードしました"
	default:
		err = fmt.Errorf("decision %s is not a transfer", plan.Decision)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return TransferView{Error: "キャンセルしました"}
		}
		s.logger.Error("transfer failed", "gameId", gameID, "err", err)
		return TransferView{Error: explainSyncError(err)}
	}
	return TransferView{Status: done}
}

func (s *SyncService) beginTransfer(gameID, token string, cancel context.CancelFunc) *gameOp {
	s.mu.Lock()
	defer s.mu.Unlock()
	op := s.ops[gameID]
	if op == nil || op.token != token || op.plan == nil || op.transferring {
		return nil
	}
	op.transferring = true
	op.cancel = cancel
	return op
}

// finish は、終わった操作がまだ今の操作のときだけ消す。転送中は新しい確認を受け付けないので、ほかの操作に置き換わってはいない。
func (s *SyncService) finish(gameID string, op *gameOp) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ops[gameID] == op {
		delete(s.ops, gameID)
	}
}

// Cancel は、転送中でなければ何もしない。
func (s *SyncService) Cancel(gameID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if op := s.ops[gameID]; op != nil && op.transferring {
		op.cancel()
	}
}

// throttleProgress は、syncore が直列に呼ぶ前提で、ロックを持たない。全体の数を知らせる最初の 1 回と、済んだ数が全体に
// 達した 1 回は、間隔によらず送る。
func throttleProgress(gameID string, emit func(ProgressEvent)) func(syncore.Progress) {
	var last time.Time
	return func(p syncore.Progress) {
		now := time.Now()
		if !last.IsZero() && p.FilesDone < p.FilesTotal && now.Sub(last) < progressInterval {
			return
		}
		last = now
		emit(ProgressEvent{GameID: gameID, Progress: p})
	}
}

func checkView(plan syncore.Plan, err error) CheckView {
	if t := plan.RemoteDeleted; t != nil {
		v := CheckView{Status: fmt.Sprintf("リモートで削除されました（%s、%s）", t.DeviceName, formatTime(t.DeletedAt))}
		if err != nil {
			// 記録はしたので、次の確認で消し残りを削除し直す（仕様書 8-X10）
			v.Error = "リモートのデータの削除に失敗しました。もう一度確認すると削除し直します: " + err.Error()
		}
		return v
	}
	if err != nil {
		return CheckView{Error: explainSyncError(err)}
	}
	switch plan.Decision {
	case syncore.DecisionPush:
		return CheckView{Status: "アップロードが必要です", Confirm: confirmView(plan, "アップロード")}
	case syncore.DecisionPull:
		return CheckView{Status: "ダウンロードが必要です", Confirm: confirmView(plan, "ダウンロード")}
	case syncore.DecisionConflict:
		if plan.LocalMissing {
			return CheckView{Status: "競合しています（ローカルにセーブが見つかりません）"}
		}
		return CheckView{Status: "競合しています"}
	default:
		return CheckView{Status: "変更なし"}
	}
}

func confirmView(plan syncore.Plan, direction string) *ConfirmView {
	rows := []InfoRow{{Label: "方向", Value: direction}}
	if plan.Remote.Hash == "" {
		rows = append(rows, InfoRow{Label: "リモート", Value: "セーブはありません"})
	} else {
		rows = append(rows,
			InfoRow{Label: "リモートを保存した PC", Value: plan.Remote.Manifest.DeviceName},
			InfoRow{Label: "リモートを保存した日時", Value: formatCreatedAt(plan.Remote.Manifest.CreatedAt)},
		)
	}
	return &ConfirmView{Title: direction + "しますか？", Rows: rows, ApproveLabel: direction}
}

// formatCreatedAt は、manifest の createdAt がほかの PC の書いた値で解釈できないこともあるので、そのときは書かれたまま出す。
func formatCreatedAt(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	return formatTime(t)
}

func formatTime(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04:05")
}

func explainSyncError(err error) string {
	switch {
	case errors.Is(err, syncore.ErrPlanStale):
		return "確認した後に状況が変わりました。もう一度確認してください"
	case errors.Is(err, syncore.ErrSyncInProgress):
		return "別の同期が実行中です。終わってからもう一度実行してください"
	case errors.Is(err, syncore.ErrGameRunning):
		return "ゲームが実行中です。終了してからもう一度実行してください"
	case errors.Is(err, syncore.ErrGameExeNotFound):
		return "登録した実行ファイルが見つかりません。ゲームの実行ファイルのパスを直してください"
	case errors.Is(err, syncore.ErrRemoteDeleted):
		return "リモートで削除されたゲームなので、同期しませんでした。もう一度確認してください"
	}
	return "失敗しました: " + err.Error()
}

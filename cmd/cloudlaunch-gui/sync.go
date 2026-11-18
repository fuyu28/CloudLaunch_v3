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
	Percent  int              `json:"percent"`
	Text     string           `json:"text"`
}

type SyncService struct {
	save   *syncore.Core
	games  *gamesync.Core
	logger *slog.Logger
	// setupErr は、リモートの接続情報か deviceName を用意できなかったときに入る。このときは確認を受け付けない
	setupErr error
	emit     func(ProgressEvent)

	mu     sync.Mutex
	ops    map[string]*gameOp
	tokens uint64
	// closed は、終了の処理を始めた後に転送を始めないために使う
	closed    bool
	transfers sync.WaitGroup
}

// gameOp は、ゲームごとの今の操作。確認中と転送中は cancel が、確認を終えて承認を待つ間は plan が入る。ダイアログを閉じても
// plan は次の確認まで捨てず、カードから開き直せるようにする。承認の後に local・remote が変わっていれば syncore が
// ErrPlanStale で止めるので、古い plan で利用者の見ていない転送はしない。
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
	// Dialog は、確認した plan があり、どちらかの向きに転送できるときに入る
	Dialog *DialogView `json:"dialog"`
}

// DialogView は、確認ダイアログ。判定どおりの同期と、上書き（仕様書 7 章）の両方をここから選ぶ。
type DialogView struct {
	// Token は、Transfer へそのまま返す
	Token string `json:"token"`
	// Open は、確認の直後に開くことを表す。判定が変更なしのときは開かず、利用者が上書きを選ぶときに開く
	Open    bool         `json:"open"`
	Title   string       `json:"title"`
	Warning string       `json:"warning"`
	Compare []CompareRow `json:"compare"`
	Actions []ActionView `json:"actions"`
}

type CompareRow struct {
	Label  string `json:"label"`
	Local  string `json:"local"`
	Remote string `json:"remote"`
}

type ActionView struct {
	Direction TransferDirection `json:"direction"`
	Label     string            `json:"label"`
	// Note は、転送で変わるものか、選べない理由
	Note     string `json:"note"`
	Disabled bool   `json:"disabled"`
	// Primary は、判定どおりの同期であることを表す
	Primary bool `json:"primary"`
}

type TransferDirection string

const (
	TransferUpload   TransferDirection = "upload"
	TransferDownload TransferDirection = "download"
)

type TransferView struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

func newSyncService(save *syncore.Core, games *gamesync.Core, logger *slog.Logger, setupErr error) *SyncService {
	return &SyncService{
		save:     save,
		games:    games,
		logger:   logger,
		setupErr: setupErr,
		emit: func(e ProgressEvent) {
			application.Get().Event.Emit(progressEvent, e)
		},
		ops: map[string]*gameOp{},
	}
}

// Check は、同じゲームの前の確認を打ち切ってから判定する。判定が base の記録に当たれば、データを転送しないので確認を取らずに
// 記録する（仕様書 4 章）。ゲームの tombstone があれば、gamesync が 8-T2 の扱いにする。
func (s *SyncService) Check(ctx context.Context, gameID string) CheckView {
	if s.setupErr != nil {
		return CheckView{Error: "同期の設定を読めません: " + s.setupErr.Error()}
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
	if view.Dialog == nil {
		delete(s.ops, gameID)
		return view
	}
	op.cancel = nil
	op.plan = &plan
	view.Dialog.Token = op.token
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
// 向きが判定と違えば上書きにする（仕様書 7 章）。承認の後に local・remote が変わったときは、syncore が ErrPlanStale で止める。
func (s *SyncService) Transfer(ctx context.Context, gameID, token string, direction TransferDirection) TransferView {
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
	var overwrite bool
	switch direction {
	case TransferUpload:
		overwrite = plan.Decision != syncore.DecisionPush
		err = s.save.Push(ctx, plan, syncore.PushOptions{Overwrite: overwrite, Progress: notify})
	case TransferDownload:
		overwrite = plan.Decision != syncore.DecisionPull
		err = s.save.Pull(ctx, plan, syncore.PullOptions{Overwrite: overwrite, Progress: notify})
	default:
		err = fmt.Errorf("unknown transfer direction %q", direction)
	}
	if err == nil {
		return TransferView{Status: transferredLabel(plan, direction, overwrite)}
	}
	if errors.Is(err, context.Canceled) {
		// 止めた後の片付け（stage やジャーナルの削除）の失敗も err に含まれうるので、キャンセルでもログに残す
		s.logger.Info("transfer canceled", "gameId", gameID, "err", err)
		return TransferView{Error: "キャンセルしました"}
	}
	s.logger.Error("transfer failed", "gameId", gameID, "err", err)
	// 承認の後にほかの PC で削除されたときは、直後に 8-T2 の扱いにする（8-T7）。Push が PUT した object を残さないため。
	// 削除で消えた object を取得できずにも失敗し、ErrRemoteDeleted にならないので、どの失敗でも確かめる。ctx は
	// キャンセルと終了で打ち切れるよう、転送のものを使う
	t, deleted, terr := s.save.HandleTombstone(ctx, gameID)
	if deleted {
		v := TransferView{Error: fmt.Sprintf("リモートで削除されました（%s、%s）", t.DeviceName, formatTime(t.DeletedAt))}
		if terr != nil {
			s.logger.Error("delete game data failed", "gameId", gameID, "err", terr)
			v.Error += "。リモートのデータの削除に失敗しました。もう一度確認すると削除し直します"
		}
		return v
	}
	if terr != nil {
		s.logger.Warn("check tombstone after transfer failed", "gameId", gameID, "err", terr)
	}
	return TransferView{Error: explainSyncError(err)}
}

func (s *SyncService) beginTransfer(gameID, token string, cancel context.CancelFunc) *gameOp {
	s.mu.Lock()
	defer s.mu.Unlock()
	op := s.ops[gameID]
	if s.closed || op == nil || op.token != token || op.plan == nil || op.transferring {
		return nil
	}
	op.transferring = true
	op.cancel = cancel
	s.transfers.Add(1)
	return op
}

// ServiceShutdown は、Wails が終了するときに、DB を閉じる前に呼ぶ。転送を打ち切り、終わるまで待つ。閉じた DB に Push・Pull の
// 後始末が当たって、中断と同じ状態を余分に残さないため。
func (s *SyncService) ServiceShutdown() error {
	s.mu.Lock()
	s.closed = true
	for _, op := range s.ops {
		if op.cancel != nil {
			op.cancel()
		}
	}
	s.mu.Unlock()
	s.transfers.Wait()
	return nil
}

// finish は、終わった操作がまだ今の操作のときだけ消す。転送中は新しい確認を受け付けないので、ほかの操作に置き換わってはいない。
func (s *SyncService) finish(gameID string, op *gameOp) {
	defer s.transfers.Done()
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
		percent := progressPercent(p)
		emit(ProgressEvent{
			GameID:   gameID,
			Progress: p,
			Percent:  percent,
			Text:     fmt.Sprintf("%d / %d ファイル（%d%%）", p.FilesDone, p.FilesTotal, percent),
		})
	}
}

// progressPercent は、空のファイルだけを転送するときもバーが進むよう、サイズの合計が 0 ならファイルの数で割る。
func progressPercent(p syncore.Progress) int {
	switch {
	case p.BytesTotal > 0:
		return int(p.BytesDone * 100 / p.BytesTotal)
	case p.FilesTotal > 0:
		return p.FilesDone * 100 / p.FilesTotal
	default:
		return 100
	}
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
	case errors.Is(err, syncore.ErrLocalMissing):
		return "ローカルにセーブがないので、アップロードで上書きできません。セーブフォルダの設定を確かめてください"
	case errors.Is(err, syncore.ErrRemoteMissing):
		return "リモートにセーブがないので、ダウンロードで上書きできません"
	}
	return "失敗しました: " + err.Error()
}

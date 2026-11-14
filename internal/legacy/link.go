// 未紐付けの旧履歴を登録したゲームと照合し、選んだ旧ゲームの旧履歴を通常のプレイ履歴として取り込む（仕様書 9 章）。
package legacy

import (
	"context"
	"errors"
	"time"
)

// Game は、未紐付けの旧履歴を旧版の gameId でまとめたもの。
type Game struct {
	ID           string
	Title        string
	Sessions     int
	TotalSec     int64
	LastPlayedAt time.Time
}

type MatchResult struct {
	// Suggested は、タイトルが 1 件だけ一致したときだけ入る（仕様書 9-K1）
	Suggested *Game
	// Candidates は、Suggested を含めた全件を、タイトル（同じなら ID）の順に持つ
	Candidates []Game
}

type LinkResult struct {
	Linked int
	// Dropped は、通常のプレイ履歴に同じ ID があったので、取り込まずに旧履歴から消した件数
	Dropped int
}

var errNotImplemented = errors.New("not implemented")

func Match(ctx context.Context, st Store, title string) (MatchResult, error) {
	return MatchResult{}, errNotImplemented
}

func Link(ctx context.Context, st Store, legacyGameID, gameID string) (LinkResult, error) {
	return LinkResult{}, errNotImplemented
}

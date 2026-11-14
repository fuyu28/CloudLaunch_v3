// 未紐付けの旧履歴を登録したゲームと照合し、選んだ旧ゲームの旧履歴を通常のプレイ履歴として取り込む（仕様書 9 章）。
package legacy

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
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
	// Candidates は、Suggested を含めた全件を、タイトル（同じなら ID）の順に持つ。提案を断ったときに選び直せるようにする
	Candidates []Game
}

type LinkResult struct {
	Linked int
	// Dropped は、通常のプレイ履歴に同じ ID があったので、取り込まずに旧履歴から消した件数
	Dropped int
}

// Match は、title と旧ゲームのタイトルを、前後の空白を除いた完全一致で比べる（仕様書 9-K4）。除いた後に空になる
// タイトルは、どれとも一致させない。
func Match(ctx context.Context, st Store, title string) (MatchResult, error) {
	games, err := st.LegacyGames(ctx)
	if err != nil {
		return MatchResult{}, fmt.Errorf("legacy games: %w", err)
	}
	if len(games) == 0 {
		return MatchResult{}, nil
	}
	slices.SortFunc(games, func(a, b Game) int {
		return cmp.Or(strings.Compare(a.Title, b.Title), strings.Compare(a.ID, b.ID))
	})

	res := MatchResult{Candidates: games}
	want := strings.TrimSpace(title)
	if want == "" {
		return res, nil
	}
	var matched []Game
	for _, g := range games {
		if strings.TrimSpace(g.Title) == want {
			matched = append(matched, g)
		}
	}
	if len(matched) == 1 {
		res.Suggested = &matched[0]
	}
	return res, nil
}

// Link は、呼べる時点を制限しない。登録の直後でなくても、登録済みのゲームならいつでも取り込める。
func Link(ctx context.Context, st Store, legacyGameID, gameID string) (LinkResult, error) {
	res, err := st.LinkLegacySessions(ctx, legacyGameID, gameID)
	if err != nil {
		return LinkResult{}, fmt.Errorf("link legacy sessions: %w", err)
	}
	return res, nil
}

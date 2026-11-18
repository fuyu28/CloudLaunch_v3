// 一覧の画面に出すデータの組み立て。表示の文言も Go で決め、Svelte は受け取ったものを出すだけにする。
package main

import (
	"context"
	"errors"

	"github.com/fuyu28/CloudLaunch_v3/internal/store"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

// HomeService は Wails のサービスとして、フロントから呼ばれる。
type HomeService struct {
	store  *store.Store
	sync   *SyncService
	banner string
}

type HomeView struct {
	Games []GameCard `json:"games"`
	// Banner は、空でなければ画面の上の帯に出す
	Banner string `json:"banner"`
	// SettingsNotice は、空でなければ、設定画面へ移るボタンと一緒に画面の上の帯に出す
	SettingsNotice string `json:"settingsNotice"`
}

type GameCard struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Brand は、未設定なら空
	Brand string `json:"brand"`
}

func (h *HomeService) Load(ctx context.Context) (HomeView, error) {
	games, err := h.store.ListGames(ctx)
	if err != nil {
		return HomeView{}, err
	}
	cards := make([]GameCard, 0, len(games))
	for _, g := range games {
		cards = append(cards, GameCard{ID: g.ID, Title: g.Title, Brand: g.Brand})
	}
	return HomeView{Games: cards, Banner: h.banner, SettingsNotice: h.sync.settingsNotice()}, nil
}

// recoverBanner は、起動時の復旧が終わらなかったときに帯に出す文言を返す。失敗した行はジャーナルに残り、次の起動で
// 復旧し直す。それまでの Pull はジャーナルの書き込み（仕様書 6-L3）で失敗するので、ここでは同期を止めない。
func recoverBanner(err error, logPath string) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, syncore.ErrSyncInProgress):
		return "別の同期が実行中だったので、起動時の復旧をしていません。同期が終わってから起動し直してください"
	default:
		return "起動時の復旧に失敗したゲームがあります。詳しくはログ（" + logPath + "）を見てください"
	}
}

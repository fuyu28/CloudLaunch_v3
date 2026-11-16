// 一覧の画面に出すデータの組み立て。表示の文言も Go で決め、Svelte は受け取ったものを出すだけにする。
package main

import (
	"context"
	"fmt"

	"github.com/fuyu28/CloudLaunch_v3/internal/store"
)

// HomeService は Wails のサービスとして、フロントから呼ばれる。
type HomeService struct {
	store *store.Store
	// recoverFailed は、起動時の復旧に失敗した行があったことを表す。失敗した行はジャーナルに残り、次の起動でも復旧を試みる
	recoverFailed bool
}

type HomeView struct {
	Games []GameCard `json:"games"`
	// Banner は、空でなければ画面の上の帯に出す
	Banner string `json:"banner"`
}

type GameCard struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Brand は、未設定なら空
	Brand string `json:"brand"`
}

func (h *HomeService) Load(ctx context.Context) (HomeView, error) {
	ids, err := h.store.GameIDs(ctx)
	if err != nil {
		return HomeView{}, err
	}
	games := make([]GameCard, 0, len(ids))
	for _, id := range ids {
		g, err := h.store.GameInfo(ctx, id)
		if err != nil {
			return HomeView{}, fmt.Errorf("read game %s: %w", id, err)
		}
		games = append(games, GameCard{ID: g.ID, Title: g.Title, Brand: g.Brand})
	}
	var banner string
	if h.recoverFailed {
		banner = "起動時の復旧に失敗したゲームがあります。詳しくはログ（logs/gui.log）を見てください"
	}
	return HomeView{Games: games, Banner: banner}, nil
}

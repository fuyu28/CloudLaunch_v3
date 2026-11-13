// Wails に公開するメソッド（Service）と、画面ごとの view model。画面に出す文言・書式・判断はここで決め、
// Svelte は受け取った値をそのまま表示する。TS の型は wails3 generate bindings が生成したものだけを使う。
// Direction・JobState は定数から TS の enum として、sync-progress のイベントの中身は RegisterEvent から生成される。
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/fuyu28/CloudLaunch_v3/internal/store"
)

func init() {
	application.RegisterEvent[ProgressView]("sync-progress")
}

type App struct {
	store *store.Store
	sync  *fakeSync
}

type SortOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type GameCardView struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Brand    string `json:"brand"`
	Initial  string `json:"initial"`
	PlayInfo string `json:"playInfo"`
}

type HomeView struct {
	SortOptions []SortOption   `json:"sortOptions"`
	Games       []GameCardView `json:"games"`
}

type NewGameInput struct {
	Title          string `json:"title"`
	Brand          string `json:"brand"`
	SaveFolderPath string `json:"saveFolderPath"`
	ExePath        string `json:"exePath"`
}

type Fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type SnapshotView struct {
	Heading string   `json:"heading"`
	Lines   []string `json:"lines"`
	Hint    string   `json:"hint"`
}

type ConfirmView struct {
	PlanID       string       `json:"planId"`
	Heading      string       `json:"heading"`
	Direction    Direction    `json:"direction"`
	Facts        []Fact       `json:"facts"`        // 競合のときは空
	Local        SnapshotView `json:"local"`        // 競合のときだけ使う
	Remote       SnapshotView `json:"remote"`       // 競合のときだけ使う
	ApproveLabel string       `json:"approveLabel"` // 承認のボタンの文言
	DeclineLabel string       `json:"declineLabel"`
}

type ProgressView struct {
	PlanID  string   `json:"planId"`
	Heading string   `json:"heading"`
	State   JobState `json:"state"`
	Percent int      `json:"percent"`
	Detail  string   `json:"detail"`
}

// emitProgress は、アプリが動き出す前（application.Get が nil）に呼ばれたら何もしない。
func (a *App) emitProgress(p Progress) {
	if app := application.Get(); app != nil {
		app.Event.Emit("sync-progress", progressView(p))
	}
}

var sortOptions = []SortOption{
	{string(store.SortTitle), "タイトル順"},
	{string(store.SortLastPlayed), "最近プレイ順"},
	{string(store.SortCreated), "登録順"},
	{string(store.SortPlayTime), "プレイ時間順"},
	{string(store.SortBrand), "ブランド順"},
}

// Home は、画面が古い呼び出しを cancel() すると ctx が打ち切られ、読み込みを途中でやめる。
func (a *App) Home(ctx context.Context, query, sort string, desc bool) (HomeView, error) {
	games, err := a.store.ListGames(ctx, query, store.GameSort(sort), desc)
	if err != nil {
		return HomeView{}, err
	}
	v := HomeView{SortOptions: sortOptions, Games: []GameCardView{}}
	for _, g := range games {
		v.Games = append(v.Games, GameCardView{
			ID:       g.ID,
			Title:    g.Title,
			Brand:    g.Brand,
			Initial:  initial(g.Title),
			PlayInfo: lastPlayed(g.LastPlayedAt) + " · " + playTime(g.TotalSec),
		})
	}
	return v, nil
}

func (a *App) AddGame(ctx context.Context, in NewGameInput) error {
	g := store.NewGame{
		Title:          strings.TrimSpace(in.Title),
		Brand:          strings.TrimSpace(in.Brand),
		SaveFolderPath: strings.TrimSpace(in.SaveFolderPath),
		ExePath:        strings.TrimSpace(in.ExePath),
		CreatedAt:      time.Now(),
	}
	if g.Title == "" || g.SaveFolderPath == "" || g.ExePath == "" {
		return errors.New("タイトル・セーブフォルダ・実行ファイルは必須です")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	g.ID = id.String()
	return a.store.CreateGame(ctx, g)
}

// Launch は、起動前の判定の代わりに偽の plan を作り、確認ダイアログの中身を返す。
func (a *App) Launch(gameID, title string) ConfirmView {
	p := a.sync.Status(gameID, title)
	v := ConfirmView{PlanID: p.ID, Direction: p.Direction}
	if p.Direction == DirectionConflict {
		v.Heading = p.GameTitle + "：セーブが競合しています"
		v.Local = SnapshotView{Heading: "この PC のセーブ", Lines: snapshotLines(p.Local), Hint: "アップロードしてクラウドを上書き"}
		v.Remote = SnapshotView{Heading: "クラウドのセーブ", Lines: snapshotLines(p.Remote), Hint: "ダウンロードしてこの PC を上書き"}
		v.ApproveLabel = "上書きする"
		v.DeclineLabel = "今回は同期しない"
		v.Facts = []Fact{}
		return v
	}
	v.Heading = p.GameTitle + "：" + directionLabel(p.Direction) + "しますか？"
	v.Facts = []Fact{
		{"変更されるファイル", fmt.Sprintf("追加 %d ・変更 %d ・削除 %d", p.Added, p.Changed, p.Deleted)},
		{"転送するサイズ", mib(p.Bytes)},
		{"クラウドのセーブ", p.Remote.Device + " ・" + dateTime(p.Remote.ModifiedAt)},
		{"この PC のセーブ", "最終更新 " + dateTime(p.Local.ModifiedAt)},
	}
	v.ApproveLabel = directionLabel(p.Direction) + "する"
	v.DeclineLabel = "しない"
	return v
}

// Approve は転送を始めてすぐに返す。以降の進捗は sync-progress のイベントで ProgressView を送る。
func (a *App) Approve(planID string, choice Direction) (ProgressView, error) {
	p, err := a.sync.Start(planID, choice)
	if err != nil {
		return ProgressView{}, err
	}
	return progressView(p), nil
}

func (a *App) Cancel(planID string) { a.sync.Cancel(planID) }

func progressView(p Progress) ProgressView {
	v := ProgressView{PlanID: p.PlanID, Heading: p.GameTitle + "：" + directionLabel(p.Direction), State: p.State}
	if p.Total > 0 {
		v.Percent = int(p.DoneBytes * 100 / p.Total)
	}
	switch p.State {
	case JobRunning:
		v.Detail = fmt.Sprintf("%s / %s（%d%%）", mib(p.DoneBytes), mib(p.Total), v.Percent)
	case JobDone:
		v.Detail = directionLabel(p.Direction) + "が終わりました。"
	case JobCancelled:
		v.Detail = "キャンセルしました。セーブは元のままです。"
	}
	return v
}

func snapshotLines(s Snapshot) []string {
	return []string{s.Device, "最終更新 " + dateTime(s.ModifiedAt), fmt.Sprintf("%d ファイル ・%s", s.Files, mib(s.Bytes))}
}

func initial(title string) string {
	for _, r := range title {
		return string(r)
	}
	return "?"
}

func playTime(sec int64) string {
	h, m := sec/3600, sec%3600/60
	if h > 0 {
		return fmt.Sprintf("%d時間%d分", h, m)
	}
	return fmt.Sprintf("%d分", m)
}

func lastPlayed(t time.Time) string {
	if t.IsZero() {
		return "未プレイ"
	}
	return dateTime(t)
}

func dateTime(t time.Time) string { return t.Local().Format("2006/01/02 15:04") }

func mib(b int64) string { return fmt.Sprintf("%.1f MB", float64(b)/(1<<20)) }

func directionLabel(d Direction) string {
	switch d {
	case DirectionDownload:
		return "ダウンロード"
	case DirectionUpload:
		return "アップロード"
	default:
		return "競合"
	}
}

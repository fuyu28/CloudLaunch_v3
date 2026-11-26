// ゲームの登録の画面（仕様書 8 章「ゲーム情報」「画像」）と、登録した後の旧履歴の照合（9-K4）。入力の検査も Go で行い、
// Svelte は欄ごとに返した文を出すだけにする。
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/fuyu28/CloudLaunch_v3/internal/legacy"
	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
)

// dialogCancelled は、Windows の Wails がダイアログのキャンセルで返すエラーの文。そのエラーの値は Wails の internal の
// パッケージにあり、errors.Is で比べられないので、文で見分ける。ほかの OS はキャンセルで空のパスを返す。
const dialogCancelled = "cancelled by user"

type GameService struct {
	store  *store.Store
	meta   *metasync.Core
	logger *slog.Logger
	// pick は、Wails のアプリを作った後に入れる。サービスはアプリより先に作るため
	pick func(opts *application.OpenFileDialogOptions) (string, error)
}

// GameForm は、登録の画面で入れた値。ImagePath が空なら画像を設定しない。
type GameForm struct {
	Title          string `json:"title"`
	Brand          string `json:"brand"`
	SaveFolderPath string `json:"saveFolderPath"`
	ExePath        string `json:"exePath"`
	ImagePath      string `json:"imagePath"`
}

// FieldCheck は、1 つの欄の検査の結果。Error があれば登録しない。Warning は出すだけで、登録は止めない。
type FieldCheck struct {
	Error   string `json:"error"`
	Warning string `json:"warning"`
}

type FormCheck struct {
	Title      FieldCheck `json:"title"`
	SaveFolder FieldCheck `json:"saveFolder"`
	Exe        FieldCheck `json:"exe"`
	Image      FieldCheck `json:"image"`
}

func (c FormCheck) ok() bool {
	return c.Title.Error == "" && c.SaveFolder.Error == "" && c.Exe.Error == "" && c.Image.Error == ""
}

// RegisterView は、登録の結果。GameID が空なら登録していない。そのときは Check か Error に理由が入る。
type RegisterView struct {
	Check  FormCheck `json:"check"`
	Error  string    `json:"error"`
	GameID string    `json:"gameId"`
	// Legacy は、未紐付けの旧履歴があるときだけ入る（仕様書 9-K4）
	Legacy *LegacyView `json:"legacy"`
	// Warning は、登録はできたが、旧履歴と照合できなかったときに入る
	Warning string `json:"warning"`
}

// LegacyView は、取り込む旧ゲームの選択肢。Suggested があれば提案として先に出し（9-K1）、なければ Candidates から選ばせる（9-K2）。
type LegacyView struct {
	Suggested *LegacyGameView `json:"suggested"`
	// Candidates は、Suggested を除いた候補
	Candidates []LegacyGameView `json:"candidates"`
}

type LegacyGameView struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Summary は、件数・合計時間・最後に遊んだ日時
	Summary string `json:"summary"`
}

type LinkView struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

func (g *GameService) CheckForm(form GameForm) FormCheck {
	c, _ := checkForm(form, true)
	return c
}

// Register は、検査し直してから登録する。画像は登録の前にキャッシュに入れ、入れられなければ登録しない。登録した後の
// 旧履歴の照合に失敗しても、登録は取り消さない。
func (g *GameService) Register(ctx context.Context, form GameForm) RegisterView {
	// 画面の再読み込みなどで呼び出しが取り消されても、登録と照合を途中で止めない。登録できたのに失敗と出さないため
	ctx = context.WithoutCancel(ctx)
	// 画像は CacheImage が同じ 8-I2 を検査するので、ここでは読まない。同じ最大 10 MiB を 2 回読まないため
	check, f := checkForm(form, false)
	if !check.ok() {
		return RegisterView{Check: check}
	}
	var sha string
	if f.ImagePath != "" {
		var err error
		if sha, err = g.cacheImage(f.ImagePath); err != nil {
			check.Image.Error = imageError(err)
			return RegisterView{Check: check}
		}
	}
	u, err := uuid.NewV7()
	if err != nil {
		return RegisterView{Error: "ゲーム ID を振れませんでした: " + err.Error()}
	}
	now := time.Now()
	info := metasync.GameInfo{ID: u.String(), Title: f.Title, Brand: f.Brand, ImageSha256: sha, CreatedAt: now, UpdatedAt: now}
	if err := g.store.RegisterGame(ctx, info, f.SaveFolderPath, f.ExePath); err != nil {
		g.logger.Error("register game failed", "err", err)
		return RegisterView{Error: "登録できませんでした: " + err.Error()}
	}

	view := RegisterView{Check: check, GameID: info.ID}
	m, err := legacy.Match(ctx, g.store, f.Title)
	if err != nil {
		g.logger.Error("match legacy sessions failed", "gameId", info.ID, "err", err)
		view.Warning = "ゲームは登録しましたが、旧版のプレイ履歴との照合に失敗しました: " + err.Error()
		return view
	}
	view.Legacy = legacyView(m)
	return view
}

func (g *GameService) cacheImage(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	return g.meta.CacheImage(f)
}

// LinkLegacy は、選ばせた旧ゲームを取り込む。押させたことを、仕様書 9-K1 の確認とみなす。
func (g *GameService) LinkLegacy(ctx context.Context, gameID, legacyGameID string) LinkView {
	ctx = context.WithoutCancel(ctx)
	res, err := legacy.Link(ctx, g.store, legacyGameID, gameID)
	if err != nil {
		g.logger.Error("link legacy sessions failed", "gameId", gameID, "legacyGameId", legacyGameID, "err", err)
		return LinkView{Error: "取り込めませんでした: " + err.Error()}
	}
	if res == (legacy.LinkResult{}) {
		// 照合の後に、別の画面や CLI で取り込んだときに当たる
		return LinkView{Error: "この旧ゲームの未紐付けのプレイ履歴はありません（取り込み済みです）"}
	}
	status := fmt.Sprintf("旧版のプレイ履歴を %d 件取り込みました", res.Linked)
	if res.Dropped > 0 {
		status += fmt.Sprintf("（既にある %d 件は取り込まずに旧履歴から消しました）", res.Dropped)
	}
	return LinkView{Status: status}
}

// PickSaveFolder・PickExe・PickImage は、キャンセルされたら空を返す。current があれば、その近くの、あるフォルダから開く。
func (g *GameService) PickSaveFolder(current string) (string, error) {
	return g.pickPath(&application.OpenFileDialogOptions{
		Title: "セーブフォルダを選ぶ", CanChooseDirectories: true, CanCreateDirectories: true, Directory: startDir(current),
	})
}

func (g *GameService) PickExe(current string) (string, error) {
	opts := &application.OpenFileDialogOptions{Title: "ゲーム本体の実行ファイルを選ぶ", CanChooseFiles: true, Directory: startDir(current)}
	if runtime.GOOS == "windows" {
		opts.Filters = []application.FileFilter{{DisplayName: "実行ファイル (*.exe)", Pattern: "*.exe"}}
	}
	return g.pickPath(opts)
}

func (g *GameService) PickImage(current string) (string, error) {
	return g.pickPath(&application.OpenFileDialogOptions{
		Title: "画像を選ぶ", CanChooseFiles: true, Directory: startDir(current),
		Filters: []application.FileFilter{{DisplayName: "画像 (*.png, *.jpg, *.webp)", Pattern: "*.png;*.jpg;*.jpeg;*.webp"}},
	})
}

// startDir は、current から親へたどり、最初にあるフォルダを返す。なければ空を返す。Windows の Wails は、開くフォルダが
// ないとダイアログを出さずにエラーを返すため。まだないセーブフォルダを入れた欄からも開けるようにする。
func startDir(current string) string {
	current = strings.TrimSpace(current)
	if current == "" || !filepath.IsAbs(current) {
		return ""
	}
	for dir := filepath.Clean(current); ; dir = filepath.Dir(dir) {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

// pickPath は、キャンセルを空のパスにそろえる。
func (g *GameService) pickPath(opts *application.OpenFileDialogOptions) (string, error) {
	p, err := g.pick(opts)
	if err != nil && err.Error() == dialogCancelled {
		return "", nil
	}
	return p, err
}

// checkForm は、前後の空白を除き、パスを filepath.Clean で整えた値も返す。登録にはこの値を使う。readImage が false なら、
// 画像は絶対パスかどうかだけを確かめる。
func checkForm(form GameForm, readImage bool) (FormCheck, GameForm) {
	f := GameForm{
		Title:          strings.TrimSpace(form.Title),
		Brand:          strings.TrimSpace(form.Brand),
		SaveFolderPath: cleanPath(form.SaveFolderPath),
		ExePath:        cleanPath(form.ExePath),
		ImagePath:      cleanPath(form.ImagePath),
	}
	var c FormCheck
	if f.Title == "" {
		c.Title.Error = "タイトルを入れてください"
	}
	c.SaveFolder = checkSaveFolder(f.SaveFolderPath)
	c.Exe = checkExe(f.ExePath)
	switch {
	case f.ImagePath == "":
	case !filepath.IsAbs(f.ImagePath):
		c.Image.Error = "絶対パスで入れてください"
	case readImage:
		c.Image = checkImageFile(f.ImagePath)
	}
	return c, f
}

func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}

// checkSaveFolder は、フォルダがなくても拒否しない。まだ遊んでいないゲームは、仕様書 4 章の「local なし」として扱える。
func checkSaveFolder(p string) FieldCheck {
	if r := checkAbs(p, "セーブフォルダ"); r.Error != "" {
		return r
	}
	info, err := os.Stat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return FieldCheck{Warning: "フォルダがありません。まだ遊んでいないゲームなら、このまま登録できます"}
	case err != nil:
		return FieldCheck{Error: "フォルダを確かめられません: " + err.Error()}
	case !info.IsDir():
		return FieldCheck{Error: "フォルダではなく、ファイルを指しています"}
	}
	return FieldCheck{}
}

// checkExe は、実行ファイルがなければ拒否する。なければ、仕様書 7 章のとおり Push も Pull も拒否されるため。.exe に
// 限るのは、ショートカットやバッチを選ぶと実行中の判定が外れるため。Linux は開発用なので拡張子を問わない。
func checkExe(p string) FieldCheck {
	if r := checkAbs(p, "実行ファイル"); r.Error != "" {
		return r
	}
	info, err := os.Stat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return FieldCheck{Error: "実行ファイルが見つかりません"}
	case err != nil:
		return FieldCheck{Error: "実行ファイルを確かめられません: " + err.Error()}
	case !info.Mode().IsRegular():
		return FieldCheck{Error: "通常のファイルではありません"}
	case runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(p), ".exe"):
		return FieldCheck{Error: "拡張子が .exe のファイルを選んでください"}
	}
	return FieldCheck{}
}

func checkAbs(p, label string) FieldCheck {
	switch {
	case p == "":
		return FieldCheck{Error: label + "を入れてください"}
	case !filepath.IsAbs(p):
		return FieldCheck{Error: "絶対パスで入れてください"}
	}
	return FieldCheck{}
}

// checkImageFile は、キャッシュに入れずに 8-I2 を検査する。選び直すたびに、使わない画像をキャッシュに増やさないため。
func checkImageFile(p string) FieldCheck {
	f, err := os.Open(p)
	if err != nil {
		return FieldCheck{Error: imageError(err)}
	}
	defer func() { _ = f.Close() }()
	if err := metasync.CheckImage(f); err != nil {
		return FieldCheck{Error: imageError(err)}
	}
	return FieldCheck{}
}

// imageError は、登録ではキャッシュへの書き込みの失敗も受け取るので、8-I2 と見つからないとき以外は、理由を決めつけずに
// エラーをそのまま出す。
func imageError(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "画像のファイルが見つかりません"
	case errors.Is(err, metasync.ErrImageTooLarge):
		return "10 MiB を超える画像は使えません"
	case errors.Is(err, metasync.ErrImageFormat):
		return "PNG・JPEG・WebP の画像を選んでください"
	}
	return "画像を使えません: " + err.Error()
}

func legacyView(m legacy.MatchResult) *LegacyView {
	if len(m.Candidates) == 0 {
		return nil
	}
	v := &LegacyView{Candidates: []LegacyGameView{}}
	if m.Suggested != nil {
		s := legacyGameView(*m.Suggested)
		v.Suggested = &s
	}
	for _, c := range m.Candidates {
		if m.Suggested != nil && c.ID == m.Suggested.ID {
			continue
		}
		v.Candidates = append(v.Candidates, legacyGameView(c))
	}
	return v
}

func legacyGameView(g legacy.Game) LegacyGameView {
	d := time.Duration(g.TotalSec) * time.Second
	return LegacyGameView{
		ID:    g.ID,
		Title: g.Title,
		Summary: fmt.Sprintf("%d 件・合計 %d 時間 %d 分・最後に遊んだ日時 %s",
			g.Sessions, int(d.Hours()), int(d.Minutes())%60, formatTime(g.LastPlayedAt)),
	}
}

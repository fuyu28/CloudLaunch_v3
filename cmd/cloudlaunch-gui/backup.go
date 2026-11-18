// ゲームのバックアップのフォルダを開く操作。v1 ではアプリから戻さず、利用者がフォルダから手で写して戻す（#130、#131）。
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

const (
	backupsGuide = "戻すときは、ゲームを終了してから、戻したいバックアップの中身をセーブフォルダへ写し、「確認」を押してください。アップロードの確認が出ます"
	noBackups    = "バックアップはまだありません。Pull で上書きする前のセーブが、ここに残ります"
)

type BackupService struct {
	save *syncore.Core
	// openFolder は、Wails のアプリを作った後に入れる。サービスはアプリより先に作るため
	openFolder func(dir string) error
}

// BackupView は、カードに出す文。どちらか一方だけが入る。
type BackupView struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

func (b *BackupService) OpenBackups(gameID string) BackupView {
	note, err := b.openBackups(gameID)
	if err != nil {
		return BackupView{Error: "バックアップのフォルダを開けませんでした: " + err.Error()}
	}
	return BackupView{Status: note}
}

// openBackups は、Windows では、explorer が窓を開けなくてもエラーを返さない。Wails が explorer の終了コードを見ないため。
func (b *BackupService) openBackups(gameID string) (string, error) {
	dir, err := b.save.BackupDir(gameID)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return noBackups, nil
	case err != nil:
		return "", err
	case !info.IsDir():
		// explorer にファイルを渡すと、関連づけたアプリでそのファイルを開いてしまう
		return "", fmt.Errorf("%s is not a folder", dir)
	}
	if err := b.openFolder(dir); err != nil {
		return "", err
	}
	return backupsGuide, nil
}

// ゲームのバックアップのフォルダを開く操作。v1 ではアプリから戻さず、利用者がフォルダから手で写して戻す（#130、#131）。
package main

import (
	"errors"
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

// OpenBackups は、フォルダを開けたら戻し方の案内を、バックアップがまだなければその旨を、カードに出す文として返す。
func (b *BackupService) OpenBackups(gameID string) (string, error) {
	dir, err := b.save.BackupDir(gameID)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return noBackups, nil
	} else if err != nil {
		return "", err
	}
	if err := b.openFolder(dir); err != nil {
		return "", err
	}
	return backupsGuide, nil
}

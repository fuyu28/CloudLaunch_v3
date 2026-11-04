// Pull で退避したセーブのバックアップ（仕様書 3 章、6 章の手順 9）。
package syncore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const (
	// RFC3339 の形は「:」を含み、Windows のファイル名に使えない（仕様書 3 章）
	backupTimeLayout = "20060102T150405.000Z"
	backupsKept      = 3
)

var errBackupsDirNotSet = errors.New("backups directory is not configured")

var backupNamePattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{3}Z-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func backupName(startedAt time.Time, opID string) string {
	return startedAt.UTC().Format(backupTimeLayout) + "-" + opID
}

// backupOld は、Pull の手順 9 と復旧の 6-R3 で共有する。startedAt と opID は Pull ジャーナルの行の値で、
// どちらから呼んでも同じバックアップ名になる。
func (c *Core) backupOld(ctx context.Context, gameID, old string, startedAt time.Time, opID string) error {
	name := backupName(startedAt, opID)
	if err := c.moveToBackup(ctx, gameID, old, name); err != nil {
		return fmt.Errorf("move old save folder to backup: %w", err)
	}
	c.pruneBackups(ctx, gameID, name)
	return nil
}

// moveToBackup は old を backups/{gameId}/{name} へ移す。リネームで移せなければ、.partial- の名前にコピーしてから
// 最終の名前にリネームし、old を削除する。最終の名前は同じボリュームの中のリネームでしか現れないので、
// 最終の名前があれば中身は完全であり、そのときは old を削除するだけにする。この順にすると、どこで中断しても、
// 完全なコピーを途中まで消えた old で上書きしない。
func (c *Core) moveToBackup(ctx context.Context, gameID, old, name string) error {
	// 空のまま Join すると、作業ディレクトリの下に移してしまう
	if c.backupsDir == "" {
		return errBackupsDirNotSet
	}
	dir := filepath.Join(c.backupsDir, gameID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create backups directory: %w", err)
	}
	dst := filepath.Join(dir, name)
	if _, err := os.Lstat(dst); err == nil {
		return removeOld(old)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat backup: %w", err)
	}

	// リネームの失敗の理由は見分けない。ボリュームの違いは OS ごとに別のエラーになり、
	// ほかの理由で失敗したのなら、コピーも同じ理由で失敗する
	renameErr := c.rename(old, dst)
	if renameErr == nil {
		return nil
	}
	partial := filepath.Join(dir, ".partial-"+name)
	if err := os.RemoveAll(partial); err != nil {
		return errors.Join(renameErr, fmt.Errorf("remove partial backup: %w", err))
	}
	// 途中までのコピーは 3 個に保つ処理の対象にならず、消さないと残り続ける
	if err := copyTree(ctx, old, partial); err != nil {
		if rmErr := os.RemoveAll(partial); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove partial backup: %w", rmErr))
		}
		return errors.Join(renameErr, fmt.Errorf("copy to backup: %w", err))
	}
	if err := c.rename(partial, dst); err != nil {
		return errors.Join(renameErr, fmt.Errorf("rename partial backup: %w", err))
	}
	return removeOld(old)
}

func removeOld(old string) error {
	if err := os.RemoveAll(old); err != nil {
		return fmt.Errorf("remove old save folder: %w", err)
	}
	return nil
}

// copyTree は src の下の通常のファイルとディレクトリを dst にコピーし、mtime を合わせる。
// 通常のファイル以外があればエラーにする（仕様書 2 章のセーブフォルダの規則と揃える）。
func copyTree(ctx context.Context, src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case isPlainDir(d.Type()):
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			return copyFile(p, target)
		default:
			return fmt.Errorf("%s is not a regular file", p)
		}
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("copy %s: %w", src, err)
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

// pruneBackups は、名前の形に合うバックアップを名前の順で新しい 3 個まで残す。created（その Pull で作ったもの）は、
// 時計が戻って名前が古くなっていても消さない（仕様書 3 章）。
// 削除に失敗しても警告を出すだけにする。古いバックアップが残っても、セーブは失われないため（仕様書 6 章の手順 9）。
func (c *Core) pruneBackups(ctx context.Context, gameID, created string) {
	dir := filepath.Join(c.backupsDir, gameID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		c.logger.WarnContext(ctx, "could not list backups", "game", gameID, "error", err)
		return
	}
	var names []string
	for _, d := range entries {
		if d.IsDir() && backupNamePattern.MatchString(d.Name()) {
			names = append(names, d.Name())
		}
	}
	// os.ReadDir は名前の順で返すので、names は古い順に並んでいる
	for _, name := range names[:max(len(names)-backupsKept, 0)] {
		if name == created {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			c.logger.WarnContext(ctx, "could not remove old backup", "game", gameID, "backup", name, "error", err)
		}
	}
}

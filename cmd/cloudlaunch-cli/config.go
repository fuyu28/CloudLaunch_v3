// config のコマンド（R2 の接続情報と deviceName、仕様書 3 章）。値の検証と保存は settings が行い、ここは端末で尋ねて出すだけにする。
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/fuyu28/CloudLaunch_v3/internal/platform"
	"github.com/fuyu28/CloudLaunch_v3/internal/settings"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
)

func openSettings(ctx context.Context, name string, args []string) (*settings.Settings, *store.Store, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	dataDir := addDataDirFlag(fs)
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	s, dir, err := openStore(ctx, *dataDir)
	if err != nil {
		return nil, nil, err
	}
	return settings.New(settings.Config{Local: s, Credentials: platform.NewCredentialStore(platform.R2CredentialTarget, dir)}), s, nil
}

// prompter は、Enter だけなら今の値を返す。GUI の設定画面が、保存した値を入力欄に入れてから保存させるのと揃える。
type prompter struct {
	in  *bufio.Reader
	out io.Writer
}

// newPrompter は、標準入力が端末でなければ拒否する。パイプで何行も流す入力は、項目の順番を取り違えやすいため。
func newPrompter(out io.Writer) (prompter, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return prompter{}, errors.New("config は端末から実行してください。値は対話で尋ねます")
	}
	return prompter{in: bufio.NewReader(os.Stdin), out: out}, nil
}

func (p prompter) ask(label, current string) (string, error) {
	if current == "" {
		_, _ = fmt.Fprintf(p.out, "%s: ", label)
	} else {
		_, _ = fmt.Fprintf(p.out, "%s [%s]: ", label, current)
	}
	line, err := p.in.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	if v := strings.TrimSpace(line); v != "" {
		return v, nil
	}
	return current, nil
}

// askSecret は、入力を画面に出さずに読む。空なら空を返し、SaveRemote が保存してある値を残す。
func (p prompter) askSecret(label string, saved bool) (string, error) {
	if saved {
		_, _ = fmt.Fprintf(p.out, "%s [保存済み]: ", label)
	} else {
		_, _ = fmt.Fprintf(p.out, "%s: ", label)
	}
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	_, _ = fmt.Fprintln(p.out)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// configRemote は、全部を尋ね終えてから SaveRemote でまとめて検証して保存する。何も変えなければ保存しない。
func configRemote(ctx context.Context, args []string, stdout io.Writer) error {
	set, s, err := openSettings(ctx, "config remote", args)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	p, err := newPrompter(stdout)
	if err != nil {
		return err
	}
	saved, err := set.SavedRemote(ctx)
	if err != nil {
		return err
	}

	in := settings.RemoteInput{}
	for _, q := range []struct {
		label   string
		current string
		dst     *string
	}{
		{"エンドポイント", saved.Endpoint, &in.Endpoint},
		{"バケット", saved.Bucket, &in.Bucket},
		{"Region（空なら auto）", saved.Region, &in.Region},
		{"アクセスキー ID", saved.AccessKeyID, &in.AccessKeyID},
	} {
		if *q.dst, err = p.ask(q.label, q.current); err != nil {
			return err
		}
	}
	if in.SecretAccessKey, err = p.askSecret("シークレットアクセスキー", saved.HasSecret); err != nil {
		return err
	}

	if in.SecretAccessKey == "" && in.Endpoint == saved.Endpoint && in.Bucket == saved.Bucket &&
		in.Region == saved.Region && in.AccessKeyID == saved.AccessKeyID {
		_, err = fmt.Fprintln(stdout, "変更しなかったので、保存しませんでした")
		return err
	}
	if err := set.SaveRemote(ctx, in); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "保存しました")
	return err
}

// configDeviceName は、何も変えなければ保存しない。保存していないときに出したホスト名を Enter だけで書くと、ホスト名を変えても
// 古い名前が残るため。
func configDeviceName(ctx context.Context, args []string, stdout io.Writer) error {
	set, s, err := openSettings(ctx, "config device-name", args)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	p, err := newPrompter(stdout)
	if err != nil {
		return err
	}
	// ホスト名が検証を通らなければ、今の値を出さずに尋ね、入れた名前を保存して直せるようにする
	current, err := set.DeviceName(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "今の deviceName を決められません: %v\n", err)
	}
	name, err := p.ask("deviceName", current)
	if err != nil {
		return err
	}
	if name == current {
		_, err = fmt.Fprintln(stdout, "変更しなかったので、保存しませんでした")
		return err
	}
	if err := set.SaveDeviceName(ctx, name); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "保存しました")
	return err
}

// configShow は、シークレットを出さず、保存してあるかだけを出す。
func configShow(ctx context.Context, args []string, stdout io.Writer) error {
	set, s, err := openSettings(ctx, "config show", args)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	r, err := set.SavedRemote(ctx)
	if err != nil {
		return err
	}
	secret := "なし"
	if r.HasSecret {
		secret = "保存済み"
	}
	region := r.Region
	if region == "" {
		region = "-（auto）"
	}
	device, err := set.DeviceName(ctx)
	if err != nil {
		device = "決められません: " + err.Error()
	}
	if _, err := fmt.Fprintf(stdout, "エンドポイント: %s\nバケット: %s\nRegion: %s\nアクセスキー ID: %s\nシークレットアクセスキー: %s\ndeviceName: %s\n",
		orDash(r.Endpoint), orDash(r.Bucket), region, orDash(r.AccessKeyID), secret, device); err != nil {
		return err
	}
	_, err = set.Remote(ctx)
	if errors.Is(err, settings.ErrRemoteNotConfigured) {
		_, err = fmt.Fprintln(stdout, "接続情報は未設定です。cloudlaunch-cli config remote で入れてください")
	}
	return err
}

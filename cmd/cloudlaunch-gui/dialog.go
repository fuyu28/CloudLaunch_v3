// 確認の結果から、カードの文言と確認ダイアログ（仕様書 7 章「確認ダイアログに出す情報」「上書き」）を組み立てる。
package main

import (
	"fmt"
	"math"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

func checkView(plan syncore.Plan, err error) CheckView {
	if t := plan.RemoteDeleted; t != nil {
		v := CheckView{Status: fmt.Sprintf("リモートで削除されました（%s、%s）", t.DeviceName, formatTime(t.DeletedAt))}
		if err != nil {
			// 記録はしたので、次の確認で消し残りを削除し直す（仕様書 8-X10）
			v.Error = "リモートのデータの削除に失敗しました。もう一度確認すると削除し直します: " + err.Error()
		}
		return v
	}
	if err != nil {
		return CheckView{Error: explainSyncError(err)}
	}
	// どちらの上書きも選べない（7-O3・7-O4）ので、開いても何もできないダイアログを出さない
	if plan.Local.Hash == "" && plan.Remote.Hash == "" {
		return CheckView{Status: "変更なし（セーブはまだありません）"}
	}
	d := &DialogView{Open: true, Compare: compareRows(plan), Actions: []ActionView{uploadAction(plan), downloadAction(plan)}}
	var status string
	switch plan.Decision {
	case syncore.DecisionPush:
		status, d.Title = "アップロードが必要です", "アップロードしますか？"
	case syncore.DecisionPull:
		status, d.Title = "ダウンロードが必要です", "ダウンロードしますか？"
	case syncore.DecisionConflict:
		status, d.Title = "競合しています", "local と remote の両方が変わっています。どちらで上書きしますか？"
		if plan.LocalMissing {
			status += "（ローカルにセーブが見つかりません）"
			d.Warning = "ローカルにセーブが見つかりません。セーブフォルダの設定を確かめてください"
		}
	default:
		status, d.Title, d.Open = "変更なし", "上書きしますか？", false
	}
	return CheckView{Status: status, Dialog: d}
}

// compareRows は、local と remote を並べる。どちらも出すのは、競合のときに見比べて上書きの向きを選ぶため。
func compareRows(plan syncore.Plan) []CompareRow {
	rows := []CompareRow{
		{Label: "保存した PC", Local: "この PC", Remote: plan.Remote.Manifest.DeviceName},
		{Label: "最終更新", Local: formatModTime(plan.Local), Remote: formatCreatedAt(plan.Remote.Manifest.CreatedAt)},
		{Label: "ファイル", Local: filesSummary(plan.Local.Entries), Remote: filesSummary(plan.Remote.Manifest.Entries)},
	}
	for i := range rows {
		if plan.Local.Hash == "" {
			rows[i].Local = "なし"
		}
		if plan.Remote.Hash == "" {
			rows[i].Remote = "なし"
		}
	}
	return rows
}

func formatModTime(l syncore.LocalState) string {
	if l.ModTime.IsZero() {
		return "—"
	}
	return formatTime(l.ModTime)
}

func filesSummary(entries []syncore.Entry) string {
	var size int64
	for _, e := range entries {
		size += e.Size
	}
	return fmt.Sprintf("%d 個（%s）", len(entries), formatBytes(size))
}

// uploadAction は、local が「なし」なら選べなくする（7-O3）。
func uploadAction(plan syncore.Plan) ActionView {
	a := ActionView{Direction: TransferUpload, Label: "アップロード", Primary: plan.Decision == syncore.DecisionPush}
	if !a.Primary {
		a.Label = "アップロードで上書き"
	}
	if plan.Local.Hash == "" {
		a.Disabled, a.Note = true, "ローカルにセーブがないので選べません"
		return a
	}
	a.Note = changesNote(plan, syncore.DirectionUpload)
	return a
}

// downloadAction は、remote が「なし」なら選べなくする（7-O4）。
func downloadAction(plan syncore.Plan) ActionView {
	a := ActionView{Direction: TransferDownload, Label: "ダウンロード", Primary: plan.Decision == syncore.DecisionPull}
	if !a.Primary {
		a.Label = "ダウンロードで上書き"
	}
	if plan.Remote.Hash == "" {
		a.Disabled, a.Note = true, "リモートにセーブがないので選べません"
		return a
	}
	a.Note = changesNote(plan, syncore.DirectionDownload)
	return a
}

// changesNote は、local と remote が同じなら、上書きでも転送しない（7-O5）ことを伝える。
func changesNote(plan syncore.Plan, d syncore.Direction) string {
	if plan.SameContent() {
		return "同じ内容なので転送しません"
	}
	c := plan.Changes(d)
	return fmt.Sprintf("追加 %d・変更 %d・削除 %d（転送 %s）", c.Added, c.Modified, c.Deleted, formatBytes(c.Bytes))
}

// transferredLabel は、上書きで local と remote が同じだったときは、コアが転送せずに base だけを記録している（7-O5）ので、
// 転送したとは出さない。
func transferredLabel(plan syncore.Plan, d TransferDirection, overwrite bool) string {
	switch {
	case overwrite && plan.SameContent():
		return "local と remote が同じなので、転送しませんでした"
	case d == TransferUpload && overwrite:
		return "アップロードで上書きしました"
	case d == TransferUpload:
		return "アップロードしました"
	case overwrite:
		return "ダウンロードで上書きしました"
	default:
		return "ダウンロードしました"
	}
}

// formatBytes は、小数第 1 位に丸めた後の値で単位を選ぶ。丸める前の値で選ぶと、1 MB の少し手前が「1024.0 KB」になる。
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value, suffix := float64(n)/unit, "KB"
	for _, s := range []string{"MB", "GB", "TB"} {
		if math.Round(value*10)/10 < unit {
			break
		}
		value, suffix = value/unit, s
	}
	return fmt.Sprintf("%.1f %s", value, suffix)
}

// 確認ダイアログに出す情報（仕様書 7 章）。転送すると変わるファイルの数え方。
package syncore

// Direction は、Changes で数える転送の向き。
type Direction int

const (
	DirectionUpload Direction = iota
	DirectionDownload
)

// Changes は、転送すると転送先で追加・変更・削除されるファイルの数と、転送するファイルの大きさの合計。
type Changes struct {
	Added    int
	Modified int
	Deleted  int
	Bytes    int64
}

// Changes は、アップロードなら remote の、ダウンロードなら local の entries を基準に、転送元の entries と path で
// 突き合わせて数える。中身が同じかは sha256 で比べる。判定によらず数えるので、上書きの確認にも使える。
// Bytes は、アップロードなら追加と変更のファイルの大きさの合計で、Push が remote の objects/ にある中身を送らない分、
// 実際より大きく出る。ダウンロードなら Pull が実際に取得する大きさ（6-F2）で、同じ中身を数え直さない。
func (p Plan) Changes(d Direction) Changes {
	from, to := p.Local.Entries, p.Remote.Manifest.Entries
	if d == DirectionDownload {
		from, to = to, from
	}
	existing := make(map[string]string, len(to))
	for _, e := range to {
		existing[e.Path] = e.SHA256
	}
	var c Changes
	var changedBytes int64
	for _, e := range from {
		sum, ok := existing[e.Path]
		switch {
		case !ok:
			c.Added++
			changedBytes += e.Size
		case sum != e.SHA256:
			c.Modified++
			changedBytes += e.Size
		}
		delete(existing, e.Path)
	}
	c.Deleted = len(existing)
	c.Bytes = changedBytes
	if d == DirectionDownload {
		c.Bytes = 0
		for _, e := range fetchEntries(p.Remote.Manifest.Entries, p.Local.Entries) {
			c.Bytes += e.Size
		}
	}
	return c
}

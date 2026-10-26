// manifest の計算と manifest ハッシュ（仕様書 2 章）。
package syncore

type Entry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Manifest struct {
	Version    int     `json:"version"`
	CreatedAt  string  `json:"createdAt"`
	DeviceName string  `json:"deviceName"`
	Entries    []Entry `json:"entries"`
}

func ManifestHash(entries []Entry) string {
	return ""
}

// R2 のアクセスキーの置き場所（仕様書 3 章）。置き場所そのものは OS ごとのファイルにある。
package platform

// R2CredentialTarget は、CLI と GUI が同じ資格情報を読むよう、両方が使う target。
const R2CredentialTarget = "CloudLaunch/R2"

// CredentialStore は、Windows では資格情報マネージャーの target の 1 件に、Linux（開発用）では dir の credential.json に置く。
// Windows では dir を、Linux では target を使わない。
type CredentialStore struct {
	target string
	dir    string
}

func NewCredentialStore(target, dir string) *CredentialStore {
	return &CredentialStore{target: target, dir: dir}
}

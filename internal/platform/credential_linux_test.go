package platform

import "testing"

// deleteTestCredential は何もしない。credential.json は t.TempDir に置くので、テストの後に消える。
func deleteTestCredential(*testing.T, *CredentialStore) {}

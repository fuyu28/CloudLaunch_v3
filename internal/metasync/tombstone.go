// ゲームの tombstone（`games/{gameId}/deleted.json`、仕様書 8 章「ゲームの tombstone の確認」）の検証。
package metasync

import (
	"encoding/json"
	"fmt"
)

// maxTombstoneSize は、読む deleted.json の本文の上限。正しい本文は 100 バイトほどなので、超えるものは壊れた object として
// 8-T4 で扱う。
const maxTombstoneSize = 64 << 10

func tombstoneKey(gameID string) string {
	return "games/" + gameID + "/deleted.json"
}

// validateTombstone は、仕様書 8-T4〜8-T6 で検証する。知らない項目は無視する。中身を使う呼び出し側（#100 の
// 「リモートで削除されました（deviceName・deletedAt）」の表示）ができたら、値も返す形にする。
func validateTombstone(body []byte) error {
	if len(body) > maxTombstoneSize {
		return fmt.Errorf("8-T4: body exceeds %d bytes", maxTombstoneSize)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return fmt.Errorf("8-T4: %w", err)
	}
	for _, name := range []string{"deletedAt", "deviceName"} {
		var s string
		if err := unmarshalString(fields, name, &s); err != nil || s == "" {
			return fmt.Errorf("8-T5: %s %s is missing or empty", name, fields[name])
		}
	}
	if _, ok := parseTimestamp(fields["deletedAt"]); !ok {
		return fmt.Errorf("8-T6: deletedAt %s is not in the spec format", fields["deletedAt"])
	}
	return nil
}

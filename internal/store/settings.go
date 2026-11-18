// アプリの設定（settings）の読み書き（仕様書 3 章「DB に持つもの」）。key の意味は settings パッケージが決める。
package store

import "context"

func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	return "", false, nil
}

func (s *Store) SetSettings(ctx context.Context, values map[string]string) error {
	return nil
}

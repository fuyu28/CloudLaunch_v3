#!/usr/bin/env bash
# 仕様書（docs/sync-spec.md）の検査できる項目の ID のうち、テストのケース名に現れないものを Markdown で一覧にする。
# テストのない ID があっても失敗させない。M2 の途中では、テストのない ID があるのが普通であるため。
#
# ID は「{章}-{種類}{番号}」（例: 4-D7）。テストのケース名は「{ID} {項目}」の形で始まるので、
# `"4-D7 ` のように、引用符と空白で前後を区切って探す。区切らないと 6-L1 が 6-L10 にも当たる。
set -euo pipefail

cd "$(dirname "$0")/.."

spec=docs/sync-spec.md
mapfile -t ids < <(grep -oE '(\*\*|\| )[0-9]+-[A-Z][0-9]+(\*\*| \|)' "$spec" | grep -oE '[0-9]+-[A-Z][0-9]+' | sort -t- -k1,1n -k2.1,2.1 -k2.2n -u)
if [ "${#ids[@]}" -eq 0 ]; then
  echo "no IDs found in $spec" >&2
  exit 1
fi

mapfile -t tests < <(git ls-files '*_test.go')
untested=()
for id in "${ids[@]}"; do
  if ! grep -qF "\"$id " "${tests[@]}"; then
    untested+=("$id")
  fi
done

echo "## テストのない仕様書の ID"
echo
echo "${#ids[@]} 個中 ${#untested[@]} 個"
echo
for id in "${untested[@]}"; do
  echo "- $id"
done

#!/bin/sh
# Клонирует закреплённые версии (versions.txt) и собирает web/vendor/. Нужны git, node и esbuild.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
src=${1:-$(mktemp -d)}
grep -v '^#' "$here/versions.txt" | while read -r repo tag; do
  [ -n "$repo" ] || continue
  d="$src/$(echo "$repo" | tr / _)"
  [ -d "$d" ] || git -c advice.detachedHead=false clone -q --depth 1 --branch "$tag" "https://github.com/$repo" "$d"
done
node "$here/build.mjs" "$src"

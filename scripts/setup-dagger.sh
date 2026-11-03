#!/bin/sh
set -eu

version=1.0.0-beta.24
bin_dir="${HOME}/.local/bin"
installer=$(mktemp)
trap 'rm -f "$installer"' 0
curl -fsSL https://dl.dagger.io/dagger/install.sh -o "$installer"
DAGGER_VERSION="$version" BIN_DIR="$bin_dir" sh "$installer"
if [ -n "${GITHUB_PATH:-}" ]; then
  echo "$bin_dir" >> "$GITHUB_PATH"
fi
"$bin_dir/dagger" version

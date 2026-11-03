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
if [ -n "${GITHUB_ENV:-}" ]; then
  # Keep CI on the runner's Docker engine, without Cloud credentials or prompts.
  {
    echo "DAGGER_ENGINE=image+docker://registry.dagger.io/engine:v$version"
    echo 'DAGGER_CLOUD_TOKEN='
    echo 'DAGGER_NO_NAG=1'
  } >> "$GITHUB_ENV"
fi
"$bin_dir/dagger" version

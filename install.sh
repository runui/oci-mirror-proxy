#!/bin/sh
set -eu

usage() {
    cat <<'EOF'
Install or update oci-mirror-proxy from https://git.zmide.com/bin/oci-mirror-proxy.

Usage: ./install.sh [--prefix DIR] [--version TAG] [--build] [--update]

Options:
  --prefix DIR   Install into DIR/bin (default: /usr/local).
  --version TAG  Install this release tag (default: VERSION environment or latest).
  --build        Compile release source instead of downloading a binary.
  --update       Replace the binary found in PATH, unless --prefix is supplied.
  -h, --help     Show this help.

Environment:
  DOWNLOAD_BASE_URL  Repository root URL for release and source downloads.
                     Default: https://git.zmide.com/bin/oci-mirror-proxy
  VERSION            Release tag (default: latest); --version takes precedence.

Detects the current OS and architecture and verifies release SHA256 checksums.
Falls back to building release source when no matching binary is published.
Source builds require Go 1.26.8 or newer (or Go toolchain auto-download).
Requires curl, tar and write access to the destination; never invokes sudo.
Configuration is preserved and running services are not restarted.
EOF
}

die() {
    printf 'error: %s\n' "$*" >&2
    exit 1
}

repo=${DOWNLOAD_BASE_URL:-https://git.zmide.com/bin/oci-mirror-proxy}
repo=${repo%/}
case "$repo" in
    https://*|http://*) ;;
    *) die 'DOWNLOAD_BASE_URL must be an HTTP or HTTPS repository root URL' ;;
esac
prefix=/usr/local
explicit_prefix=false
version=${VERSION:-latest}
build=false
update=false
while [ "$#" -gt 0 ]; do
    case "$1" in
        --prefix|--version)
            [ "$#" -ge 2 ] && [ -n "$2" ] || die "$1 requires a value"
            case "$1" in
                --prefix) prefix=$2; explicit_prefix=true ;;
                --version) version=$2 ;;
            esac
            shift 2
            ;;
        --build) build=true; shift ;;
        --update) update=true; shift ;;
        -h|--help) usage; exit 0 ;;
        *) die "unknown option: $1 (see --help)" ;;
    esac
done

case "$prefix" in
    /*) ;;
    *) die '--prefix must be an absolute path' ;;
esac
validate_version() {
    case "$version" in
        ''|[!a-zA-Z0-9]*|*[!a-zA-Z0-9._+-]*) die 'invalid release tag' ;;
    esac
}
validate_version
for tool in curl tar install mktemp; do
    command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done

bin_dir=${prefix%/}/bin
if [ "$update" = true ] && [ "$explicit_prefix" = false ]; then
    existing=$(command -v oci-mirror-proxy) || die 'no installed binary found in PATH; specify --prefix or install without --update'
    case "$existing" in
        /*) ;;
        *) existing="$PWD/$existing" ;;
    esac
    bin_dir=$(CDPATH= cd -- "$(dirname -- "$existing")" && pwd)
fi
destination="$bin_dir/oci-mirror-proxy"
[ ! -L "$destination" ] || die "refusing to replace symlink $destination; specify the real installation prefix"

build_dir=$(mktemp -d "${TMPDIR:-/tmp}/oci-mirror-proxy-install.XXXXXXXX")
staged_binary=
cleanup() {
    rm -rf "$build_dir"
    if [ -n "$staged_binary" ]; then rm -f "$staged_binary"; fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

if [ "$version" = latest ]; then
    release_url=$(curl --fail --silent --show-error --location --output /dev/null \
        --write-out '%{url_effective}' "$repo/releases/latest")
    # Gitea redirects this page to /releases/tag/<version>; no API is needed.
    case "$release_url" in
        */releases/tag/*) version=${release_url##*/releases/tag/} ;;
        *) die 'cannot resolve latest release; specify --version or VERSION explicitly' ;;
    esac
    validate_version
fi

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
esac

if [ "$build" = false ]; then
    case "$os/$arch" in
        linux/amd64|linux/arm64)
            asset="oci-mirror-proxy_${version}_${os}_${arch}.tar.gz"
            printf 'Downloading %s...\n' "$asset"
            status=$(curl --silent --show-error --location --output "$build_dir/$asset" \
                --write-out '%{http_code}' "$repo/releases/download/$version/$asset")
            case "$status" in
                200)
                    curl --fail --silent --show-error --location \
                        "$repo/releases/download/$version/SHA256SUMS" > "$build_dir/SHA256SUMS"
                    expected=$(awk -v name="$asset" '$2 == name || $2 == "./" name { print $1 }' "$build_dir/SHA256SUMS")
                    [ "${#expected}" -eq 64 ] || die "missing or invalid checksum for $asset"
                    case "$expected" in *[!0-9a-fA-F]*) die 'invalid SHA256 checksum' ;; esac
                    if command -v sha256sum >/dev/null 2>&1; then
                        actual=$(sha256sum "$build_dir/$asset")
                    elif command -v shasum >/dev/null 2>&1; then
                        actual=$(shasum -a 256 "$build_dir/$asset")
                    else
                        die 'sha256sum or shasum is required to verify downloads'
                    fi
                    [ "${actual%% *}" = "$expected" ] || die 'SHA256 verification failed; installation aborted'
                    tar -xzf "$build_dir/$asset" -C "$build_dir" ./oci-mirror-proxy
                    ;;
                404) build=true ;;
                *) die "binary download failed (HTTP $status); installation aborted" ;;
            esac
            ;;
        *) build=true ;;
    esac
fi

if [ "$build" = true ]; then
    command -v go >/dev/null 2>&1 || die 'no matching binary available or --build selected; install Go to compile from source'
    printf 'Building release %s for %s/%s...\n' "$version" "$os" "$arch"
    curl --fail --silent --show-error --location "$repo/archive/$version.tar.gz" > "$build_dir/source.tar.gz"
    mkdir "$build_dir/source"
    tar -xzf "$build_dir/source.tar.gz" -C "$build_dir/source"
    set -- "$build_dir"/source/*
    [ "$#" -eq 1 ] && [ -f "$1/go.mod" ] || die 'unexpected source archive layout'
    (
        cd "$1"
        CGO_ENABLED=0 GOOS=$(go env GOHOSTOS) GOARCH=$(go env GOHOSTARCH) go build -trimpath \
            -ldflags "-s -w -X github.com/runui/oci-mirror-proxy/internal/cli.version=$version" \
            -o "$build_dir/oci-mirror-proxy" ./cmd/oci-mirror-proxy
    )
fi

mkdir -p "$bin_dir" || die "cannot create $bin_dir; try --prefix \"\$HOME/.local\""
# Rename on the same filesystem so updating a running executable is safe.
staged_binary=$(mktemp "$bin_dir/.oci-mirror-proxy.XXXXXXXX")
install -m 755 "$build_dir/oci-mirror-proxy" "$staged_binary"
mv -f "$staged_binary" "$destination"
staged_binary=
printf 'Installed %s (%s)\n' "$destination" "$version"
if [ "$update" = true ]; then
    printf 'Configuration preserved. Restart any running proxy service to use the update.\n'
else
    printf '\nAdd the installation directory to PATH, then run:\n'
    printf '  oci-mirror-proxy init\n'
    printf '  oci-mirror-proxy serve\n'
fi

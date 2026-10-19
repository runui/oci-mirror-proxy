#!/bin/sh
set -eu

usage() {
    cat <<'EOF'
Build and install oci-mirror-proxy from this source checkout.

Usage: ./install.sh [--prefix DIR] [--version VERSION]

Options:
  --prefix DIR       Install into DIR/bin (default: /usr/local).
  --version VERSION  Embed this version (default: VERSION environment or dev).
  -h, --help         Show this help.

Requires Go 1.26.8 or newer (or Go toolchain auto-download) and write access
to the installation directory. No sudo is invoked automatically.
Existing configuration and Docker settings are never changed.
EOF
}

die() {
    printf 'error: %s\n' "$*" >&2
    exit 1
}

prefix=/usr/local
version=${VERSION:-dev}
while [ "$#" -gt 0 ]; do
    case "$1" in
        --prefix|--version)
            [ "$#" -ge 2 ] && [ -n "$2" ] || die "$1 requires a value"
            case "$1" in
                --prefix) prefix=$2 ;;
                --version) version=$2 ;;
            esac
            shift 2
            ;;
        -h|--help) usage; exit 0 ;;
        *) die "unknown option: $1 (see --help)" ;;
    esac
done

case "$prefix" in
    /*) ;;
    *) die '--prefix must be an absolute path' ;;
esac
# The linker parses -X values as words, so reject whitespace in version labels.
case "$version" in
    *[!a-zA-Z0-9._+-]*) die '--version may contain only letters, digits, dots, underscores, pluses and hyphens' ;;
esac

command -v go >/dev/null 2>&1 || die 'Go is required; install Go or enable it in PATH first'
command -v install >/dev/null 2>&1 || die 'the install command is required'
source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
build_dir=$(mktemp -d "${TMPDIR:-/tmp}/oci-mirror-proxy-install.XXXXXXXX")
trap 'rm -rf "$build_dir"' EXIT
trap 'exit 1' HUP INT TERM

printf 'Building oci-mirror-proxy (%s)...\n' "$version"
(
    cd "$source_dir"
    CGO_ENABLED=0 go build -trimpath \
        -ldflags "-s -w -X github.com/runui/oci-mirror-proxy/internal/cli.version=$version" \
        -o "$build_dir/oci-mirror-proxy" ./cmd/oci-mirror-proxy
)

bin_dir=${prefix%/}/bin
mkdir -p "$bin_dir" || die "cannot create $bin_dir; try --prefix \"\$HOME/.local\""
install -m 755 "$build_dir/oci-mirror-proxy" "$bin_dir/oci-mirror-proxy" ||
    die "cannot install into $bin_dir; check permissions or use --prefix \"\$HOME/.local\""

printf 'Installed %s\n' "$bin_dir/oci-mirror-proxy"
printf '\nNext steps (ensure the installation directory is in PATH):\n'
printf '  oci-mirror-proxy init\n'
printf '  oci-mirror-proxy serve\n'
printf '\nDocker integration is manual; see docs/operations.md.\n'

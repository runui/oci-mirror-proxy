#!/bin/sh
set -eu

usage() {
    cat <<'EOF'
Install or update oci-mirror-proxy from https://git.zmide.com/bin/oci-mirror-proxy.

Usage: ./install.sh [install|update|uninstall] [--prefix DIR] [--version TAG] [--build] [--update] [--service] [--skip-docker]
                    [-- REMOTE_UPDATE_ARGS...]

Options:
  install        Install (default). Existing parameter-only calls remain supported.
  update         Update; --update remains a supported alias.
  uninstall      Remove the selected binary and its managed service; preserve data.
                  Service removal first removes this proxy from Docker settings;
                  --skip-docker explicitly leaves Docker settings unchanged.
  --prefix DIR   Install into DIR/bin (default: /usr/local).
  --version TAG  Install this release tag (default: VERSION environment or latest).
  --build        Compile release source instead of downloading a binary.
  --update       Replace the binary found in PATH, unless --prefix is supplied.
  --service      Install and start a systemd/OpenRC/launchd service (sudo as needed).
  --skip-docker  Skip Docker setup.
                 Binary-only installs always skip Docker setup.
                 Service installs apply local Docker settings without restarting
                 Docker; updates only maintain integration that is still managed.
  --             Pass remaining arguments to remote update.
  -h, --help     Show this help.

Environment:
  DOWNLOAD_BASE_URL  Repository root URL for release and source downloads.
                     Default: https://git.zmide.com/bin/oci-mirror-proxy
  VERSION            Release tag (default: latest); --version takes precedence.

Detects the current OS and architecture and verifies release SHA256 checksums.
Falls back to building release source when no matching binary is published.
Source builds require Go 1.26.8 or newer (or Go toolchain auto-download).
Requires curl and tar. Only system changes request sudo; downloads and builds do not.
Configuration is preserved; services are only restarted with --service.
EOF
}

die() {
    printf 'error: %s\n' "$*" >&2
    exit 1
}

# Keep downloads/builds in the caller's account. Only operations on the selected
# installation or system service cross this boundary; never evaluate shell text.
run_system() {
    if [ "$needs_sudo" = true ]; then sudo -- "$@"; else "$@"; fi
}

run_docker_user() {
    if [ "$(id -u)" -eq 0 ]; then
        sudo -H -u "$docker_user" -- "$@"
    else
        "$@"
    fi
}

run_config() {
    if [ "$manager" = launchd ]; then
        run_system env -u XDG_CONFIG_HOME -u XDG_CACHE_HOME HOME=/var/root XDG_RUNTIME_DIR=/var/run "$@"
    elif [ -n "$manager" ]; then
        run_system "$@"
    elif [ -n "$config_user" ]; then
        sudo -H -u "$config_user" -- env -u XDG_CONFIG_HOME -u XDG_CACHE_HOME -u XDG_RUNTIME_DIR HOME="$config_home" "$@"
    else
        "$@"
    fi
}

init_config() {
    init_path=$1
    shift
    run_config "$destination" init --config "$init_path" "$@" || return 1
}

sync_remote() {
    [ -z "$sync_skip" ] || { printf '%s\n' "$sync_skip" >&2; return 1; }
    # Subscriptions remain a client operation; persistence goes through the
    # running service's config.d API, never direct privileged file writes.
    if [ -n "$manager" ]; then
        run_config "$destination" remote update "$@" --admin-socket "$service_socket"
    else
        run_config "$destination" remote update "$@"
    fi
}

service_socket=/run/oci-mirror-proxy/admin.sock
[ "$(uname -s)" != Darwin ] || service_socket=/var/run/oci-mirror-proxy/admin.sock

action=install
explicit_action=false
case "${1:-}" in
    install|update|uninstall) action=$1; explicit_action=true; shift ;;
esac
repo=${DOWNLOAD_BASE_URL:-https://git.zmide.com/bin/oci-mirror-proxy}
repo=${repo%/}
case "$action:$repo" in
    uninstall:*) ;;
    *:https://*|*:http://*) ;;
    *) die 'DOWNLOAD_BASE_URL must be an HTTP or HTTPS repository root URL' ;;
esac
prefix=/usr/local
explicit_prefix=false
version=${VERSION:-latest}
build=false
update=false
[ "$action" != update ] || update=true
service=false
skip_docker=false
while [ "$#" -gt 0 ]; do
    case "$1" in
        --prefix|--version)
            [ "$action" != uninstall ] || [ "$1" != --version ] || die '--version is not valid for uninstall'
            [ "$#" -ge 2 ] && [ -n "$2" ] || die "$1 requires a value"
            case "$1" in
                --prefix) prefix=$2; explicit_prefix=true ;;
                --version) version=$2 ;;
            esac
            shift 2
            ;;
        --build) [ "$action" != uninstall ] || die '--build is not valid for uninstall'; build=true; shift ;;
        --update)
            [ "$explicit_action" = false ] || [ "$action" = update ] || die '--update conflicts with the selected command'
            action=update; update=true; shift ;;
        --service) service=true; shift ;;
        --skip-docker) skip_docker=true; shift ;;
        --) [ "$action" != uninstall ] || die 'uninstall does not accept remote update arguments'; shift; break ;;
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
if [ "$action" != uninstall ]; then validate_version; fi
tools='mktemp'
if [ "$action" != uninstall ]; then tools='curl tar install mktemp'; fi
for tool in $tools; do
    command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done

bin_dir=${prefix%/}/bin
if [ "$action" = uninstall ] && [ "$explicit_prefix" = false ]; then
    existing=$(command -v oci-mirror-proxy || true)
    if [ -n "$existing" ]; then
        case "$existing" in /*) ;; *) existing="$PWD/$existing" ;; esac
        bin_dir=$(CDPATH= cd -- "$(dirname -- "$existing")" && pwd)
    fi
fi
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

manager=
needs_sudo=false
config_user=
config_home=
sync_skip=
os=$(uname -s | tr '[:upper:]' '[:lower:]')
# Detect definitions even when their manager is not running. Never delete a
# binary while leaving an undiscovered service pointing at it.
if [ "$action" = uninstall ]; then
    detected_binary=
    for candidate in /etc/systemd/system/oci-mirror-proxy.service /etc/init.d/oci-mirror-proxy /Library/LaunchDaemons/com.runui.oci-mirror-proxy.plist; do
        if [ -e "$candidate" ] || [ -L "$candidate" ]; then
            [ ! -L "$candidate" ] || die "refusing service symlink $candidate"
            [ -z "$detected_binary" ] || die 'multiple service definitions found; manage them manually'
            case "$candidate" in
                *.service) detected_binary=$(sed -n 's/^ExecStart=\([^ ]*\) serve .*/\1/p' "$candidate") ;;
                *.plist) detected_binary=$(sed -n 's|^<string>\([^<]*\)</string><string>serve</string>$|\1|p' "$candidate") ;;
                *) detected_binary=$(sed -n 's/^command="\([^"]*\)"$/\1/p' "$candidate") ;;
            esac
            [ -n "$detected_binary" ] || die "cannot identify managed binary from $candidate"
            case "$detected_binary" in /*/oci-mirror-proxy) ;; *) die 'invalid service executable path' ;; esac
            # A user-selected prefix may be a separate binary-only installation.
            # Never stop an unrelated system service while removing that binary.
            if [ "$explicit_prefix" = true ] && [ "$destination" != "$detected_binary" ]; then
                detected_binary=
                continue
            fi
            if [ "$explicit_prefix" = false ]; then
                if [ -n "${existing:-}" ] && [ "$destination" != "$detected_binary" ]; then
                    die 'PATH and service select different installations; specify --prefix'
                fi
                destination=$detected_binary
                bin_dir=${destination%/*}
            fi
            service=true
        fi
    done
fi
# A privileged binary install must not create private root-owned files in the
# invoking user's home. Resolve the account rather than trusting inherited HOME.
if [ "$service" = false ] && [ "$(id -u)" -eq 0 ]; then
    if [ -n "${SUDO_USER:-}${SUDO_UID:-}" ] && [ "${SUDO_UID:-}" != 0 ]; then
        if [ -n "${SUDO_USER:-}" ] && [ "${SUDO_UID:-0}" != 0 ] &&
            [ "$(id -u "$SUDO_USER" 2>/dev/null)" = "${SUDO_UID:-}" ] &&
            [ "$(id -g "$SUDO_USER" 2>/dev/null)" = "${SUDO_GID:-}" ] && command -v sudo >/dev/null 2>&1; then
            config_user=$SUDO_USER
            config_home=$(sudo -H -u "$config_user" -- sh -c 'printf "%s" "$HOME"') || config_home=
            case "$config_home" in /*) ;; *) sync_skip='Cannot resolve the invoking user home; run remote update as that user.' ;; esac
            HOME=$config_home
            unset XDG_CONFIG_HOME XDG_CACHE_HOME XDG_RUNTIME_DIR
        else
            sync_skip='Cannot validate the sudo account; run remote update as the intended user.'
        fi
    else
        # Direct root installs use root's account home, never an inherited user home.
        config_home=$(cd ~root && pwd) || die 'cannot resolve root home'
        HOME=$config_home
        export HOME
        unset XDG_CONFIG_HOME XDG_CACHE_HOME XDG_RUNTIME_DIR
    fi
fi
if [ "$service" = true ]; then
    case "$os" in linux|darwin) ;; *) die '--service requires Linux or macOS' ;; esac
    if [ "$(id -u)" -ne 0 ]; then
        command -v sudo >/dev/null 2>&1 || die 'system service management requires sudo or a root session'
        needs_sudo=true
    fi
    # Service definitions interpret paths themselves; only accept literal safe paths.
    case "$destination" in
        *[!a-zA-Z0-9/._+-]*) die '--service requires an installation path without spaces or special characters' ;;
    esac
    if [ "$os" = darwin ]; then
        command -v launchctl >/dev/null 2>&1 || die 'launchctl is required'
        command -v plutil >/dev/null 2>&1 || die 'plutil is required'
        manager=launchd
        service_label=com.runui.oci-mirror-proxy
        service_file=/Library/LaunchDaemons/$service_label.plist
    elif [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
        manager=systemd
        service_file=/etc/systemd/system/oci-mirror-proxy.service
    elif [ -d /run/openrc ] && command -v rc-service >/dev/null 2>&1 && command -v rc-update >/dev/null 2>&1; then
        manager=openrc
        service_file=/etc/init.d/oci-mirror-proxy
    else
        [ "$action" != uninstall ] || die 'service definition exists but its service manager is unavailable; manage it manually'
        printf 'Warning: no running systemd or OpenRC detected; installing binary only.\n' >&2
        sync_skip='No service manager detected; run remote update as the intended user.'
    fi
fi

if [ "$needs_sudo" = false ] && [ "$(id -u)" -ne 0 ]; then
    writable_parent=$bin_dir
    while [ ! -e "$writable_parent" ] && [ ! -L "$writable_parent" ]; do writable_parent=$(dirname -- "$writable_parent"); done
    if [ ! -w "$writable_parent" ]; then
        command -v sudo >/dev/null 2>&1 || die "cannot write $bin_dir; use --prefix \"\$HOME/.local\" or install sudo"
        needs_sudo=true
    fi
fi

# Docker Desktop belongs to the invoking user, not the root service account.
# Resolve that identity before launchd normalizes HOME for the daemon commands.
docker_user=
docker_home=
docker_identity_error=
docker_endpoint=
if [ "$service" = true ] && [ "$os" = linux ] && [ "$skip_docker" = false ]; then
    # sudo normally changes HOME, hiding the caller's active Docker context.
    # Resolve it as the caller instead of silently enrolling a different engine.
    # Root invoking sudo is valid too; validate the account, not a nonzero UID.
    if [ "$(id -u)" -ne 0 ] || [ -z "${SUDO_USER:-}" ]; then
        if [ -n "${DOCKER_HOST:-}" ] && [ -z "${DOCKER_CONTEXT:-}" ]; then
            docker_endpoint=$DOCKER_HOST
        else
            docker_endpoint=$(docker context inspect --format '{{.Endpoints.docker.Host}}') ||
                docker_identity_error='cannot inspect the Docker context'
        fi
    elif [ "$(id -u "$SUDO_USER" 2>/dev/null)" = "${SUDO_UID:-}" ] &&
        [ "$(id -g "$SUDO_USER" 2>/dev/null)" = "${SUDO_GID:-}" ]; then
        if [ -n "${DOCKER_HOST:-}" ] && [ -z "${DOCKER_CONTEXT:-}" ]; then
            docker_endpoint=$DOCKER_HOST
        else
            docker_endpoint=$(sudo -H -u "$SUDO_USER" -- docker context inspect --format '{{.Endpoints.docker.Host}}') ||
                docker_identity_error='cannot inspect the invoking user Docker context'
        fi
    else
        docker_identity_error='cannot validate the invoking Docker user'
    fi
    case "$docker_endpoint" in
        unix:///run/user/*) docker_identity_error='automatic system service enrollment does not support rootless Docker; configure it as the Docker user' ;;
    esac
fi
if [ "$service" = true ] && [ "$os" = darwin ] && [ "$skip_docker" = false ]; then
    if [ "$(id -u)" -ne 0 ]; then
        docker_user=$(id -un)
        docker_home=$HOME
    elif [ -n "${SUDO_USER:-}" ] && [ "${SUDO_UID:-0}" != 0 ] &&
        [ "$(id -u "$SUDO_USER" 2>/dev/null)" = "${SUDO_UID:-}" ] &&
        [ "$(id -g "$SUDO_USER" 2>/dev/null)" = "${SUDO_GID:-}" ] && command -v sudo >/dev/null 2>&1; then
        docker_user=$SUDO_USER
        docker_home=$(sudo -H -u "$docker_user" -- sh -c 'printf "%s" "$HOME"') || docker_home=
        case "$docker_home" in /*) ;; *) docker_identity_error='cannot resolve the Docker Desktop user home' ;; esac
    else
        docker_identity_error='cannot identify the invoking Docker Desktop user; rerun with sudo from that user or use --skip-docker'
    fi
fi

build_dir=$(mktemp -d "${TMPDIR:-/tmp}/oci-mirror-proxy-install.XXXXXXXX")
staged_binary=
rollback=false
cleanup() {
    # Cleanup failures are warnings: they must not replace the installation's
    # original exit status (or turn optional Docker failure into install failure).
    cleanup_status=$?
    trap - EXIT
    set +e
    if [ "$rollback" = true ]; then
        printf 'Restoring the previous launchd installation after failure...\n' >&2
        run_system launchctl bootout "system/$service_label" >/dev/null 2>&1 || true
        if [ -f "$build_dir/previous-binary" ]; then
            restore_binary=$(run_system mktemp "$bin_dir/.oci-mirror-proxy.XXXXXXXX") &&
                run_system cp -p "$build_dir/previous-binary" "$restore_binary" &&
                run_system mv -f "$restore_binary" "$destination" || printf 'Warning: binary restore failed.\n' >&2
        else
            run_system rm -f "$destination"
        fi
        if [ -f "$build_dir/previous-service" ]; then
            run_system cp -p "$build_dir/previous-service" "$service_file" || printf 'Warning: service definition restore failed.\n' >&2
            if [ "$previous_loaded" = true ]; then
                run_system launchctl bootstrap system "$service_file" || printf 'Warning: previous service restart failed.\n' >&2
            fi
        else
            run_system rm -f "$service_file"
        fi
        if [ "$previous_disabled" = true ]; then
            run_system launchctl disable "system/$service_label" || printf 'Warning: disabled state restore failed.\n' >&2
        fi
    fi
    rm -rf "$build_dir" || printf 'Warning: could not remove temporary directory %s\n' "$build_dir" >&2
    if [ -n "$staged_binary" ]; then
        run_system rm -f "$staged_binary" || printf 'Warning: could not remove staged executable %s\n' "$staged_binary" >&2
    fi
    if [ -n "${handoff_dir:-}" ]; then
        rm -rf "$handoff_dir" || printf 'Warning: could not remove handoff directory %s\n' "$handoff_dir" >&2
    fi
    exit "$cleanup_status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

if [ "$manager" = systemd ]; then
    cat > "$build_dir/service" <<EOF
# Managed by oci-mirror-proxy install.sh
[Unit]
Description=OCI registry mirror proxy
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
ExecStart=$destination serve --config /etc/oci-mirror-proxy/config.yaml
Restart=on-failure
RestartSec=5
RuntimeDirectory=oci-mirror-proxy
RuntimeDirectoryMode=0700
UMask=0077

[Install]
WantedBy=multi-user.target
EOF
elif [ "$manager" = openrc ]; then
    cat > "$build_dir/service" <<EOF
#!/sbin/openrc-run
# Managed by oci-mirror-proxy install.sh
description="OCI registry mirror proxy"
command="$destination"
command_args="serve --config /etc/oci-mirror-proxy/config.yaml"
command_background=true
pidfile="/run/oci-mirror-proxy/service.pid"
output_log="/var/log/oci-mirror-proxy.log"
error_log="/var/log/oci-mirror-proxy.log"
umask=0077

depend() {
    need net
}

start_pre() {
    checkpath --directory --mode 0700 --owner root:root /run/oci-mirror-proxy
}
EOF
elif [ "$manager" = launchd ]; then
    # Do not follow redirected privileged directories into user-controlled data.
    for service_dir in /Library/LaunchDaemons /var/log/oci-mirror-proxy /etc/oci-mirror-proxy; do
        [ ! -L "$service_dir" ] || die "refusing service directory symlink $service_dir"
    done
    for log_file in /var/log/oci-mirror-proxy/stdout.log /var/log/oci-mirror-proxy/stderr.log; do
        [ ! -L "$log_file" ] || die "refusing service log symlink $log_file"
    done
    cat > "$build_dir/service" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Managed by oci-mirror-proxy install.sh -->
<plist version="1.0"><dict>
<key>Label</key><string>$service_label</string>
<key>ProgramArguments</key><array>
<string>$destination</string><string>serve</string>
<string>--config</string><string>/etc/oci-mirror-proxy/config.yaml</string>
</array>
<key>WorkingDirectory</key><string>/</string>
<key>EnvironmentVariables</key><dict>
<key>HOME</key><string>/var/root</string>
<key>XDG_RUNTIME_DIR</key><string>/var/run</string>
</dict>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>ThrottleInterval</key><integer>5</integer>
<key>Umask</key><integer>63</integer>
<key>StandardOutPath</key><string>/var/log/oci-mirror-proxy/stdout.log</string>
<key>StandardErrorPath</key><string>/var/log/oci-mirror-proxy/stderr.log</string>
</dict></plist>
EOF
    plutil -lint "$build_dir/service" >/dev/null || die 'invalid launchd service definition'
fi
if [ -n "$manager" ]; then
    # Preserve custom definitions, including edits to a previously generated file.
    [ ! -L "$service_file" ] || die "refusing to replace service symlink $service_file"
    if [ -e "$service_file" ] && ! cmp -s "$build_dir/service" "$service_file"; then
        # Accept the exact previous installer definition, not arbitrary user edits.
        sed 's|--config /etc/oci-mirror-proxy/config.yaml|--config /etc/oci-mirror-proxy/config.yaml --admin-socket /run/oci-mirror-proxy/admin.sock|' \
            "$build_dir/service" > "$build_dir/legacy-service"
        cmp -s "$build_dir/legacy-service" "$service_file" ||
            die "$service_file differs from the generated definition; manage this service manually (omit --service)"
    fi
fi

if [ "$action" = uninstall ]; then
    # Preflight before touching the service. Configuration
    # and logs are deliberately outside the uninstall deletion set.
    target=$destination
    while [ "$target" != / ]; do
        # macOS ships /var as a root-owned alias for /private/var.
        if [ "$os" = darwin ] && [ "$target" = /var ] && [ "$(readlink /var)" = private/var ]; then break; fi
        [ ! -L "$target" ] || die "refusing symlink $target"
        target=$(dirname -- "$target")
    done
    [ ! -e "$destination" ] || [ -f "$destination" ] || die 'installed binary is not a regular file'
    config=/etc/oci-mirror-proxy/config.yaml
    if [ "$skip_docker" = true ]; then
        printf 'Warning: Docker cleanup skipped; settings may still reference this proxy.\n' >&2
    elif [ -n "$manager" ]; then
        [ -x "$destination" ] || die 'cannot clean up Docker without the executable; restore it or explicitly use --skip-docker'
        [ -z "$docker_identity_error" ] || die "Docker cleanup aborted: $docker_identity_error; installation preserved; use --skip-docker to opt out"
        printf 'Removing this proxy from Docker configuration before stopping the service...\n'
        if [ "$os" = darwin ]; then
            run_docker_user "$destination" dockerd revert --admin-socket "$service_socket" --no-reload ||
                die 'Docker cleanup failed; service and executable preserved. Retry dockerd revert as the Docker user, or rerun uninstall with --skip-docker'
        else
            # Linux daemon settings are system-owned; preserve the caller's
            # selected endpoint rather than detecting root's Docker context.
            case "$docker_endpoint" in unix:///*) ;; *) die 'Docker cleanup requires a local Unix Docker endpoint; use --skip-docker to opt out' ;; esac
            run_system "$destination" dockerd revert --admin-socket "$service_socket" --docker-socket "${docker_endpoint#unix://}" --no-reload ||
                die 'Docker cleanup failed; service and executable preserved. Retry dockerd revert, or rerun uninstall with --skip-docker'
        fi
        printf 'Docker configuration cleanup completed. Docker was not restarted; restart Docker to load any changes.\n'
    fi
    case "$manager" in
        systemd)
            run_system systemctl stop oci-mirror-proxy.service
            run_system systemctl disable oci-mirror-proxy.service
            run_system rm -f "$service_file"
            run_system systemctl daemon-reload ;;
        openrc)
            if run_system rc-service oci-mirror-proxy status >/dev/null 2>&1; then run_system rc-service oci-mirror-proxy stop; fi
            run_system rc-update del oci-mirror-proxy default
            run_system rm -f "$service_file" ;;
        launchd)
            if launchctl print "system/$service_label" >/dev/null 2>&1; then run_system launchctl bootout "system/$service_label"; fi
            run_system launchctl enable "system/$service_label"
            run_system rm -f "$service_file" ;;
    esac
    run_system rm -f "$destination"
    printf 'Uninstalled %s. Configuration, caches and logs preserved. Docker was not restarted.\n' "$destination"
    exit 0
fi

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
    armv7l|armv7|armhf) arch=armv7 ;;
esac

if [ "$build" = false ]; then
    case "$os/$arch" in
        linux/amd64|linux/arm64|linux/armv7|linux/riscv64|darwin/amd64|darwin/arm64|freebsd/amd64|freebsd/arm64)
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
                    # GoReleaser uses a bare member name; older releases used ./.
                    member=$(tar -tzf "$build_dir/$asset" | awk '$0 == "oci-mirror-proxy" || $0 == "./oci-mirror-proxy" { print; exit }')
                    [ -n "$member" ] || die 'release archive does not contain oci-mirror-proxy'
                    tar -xzf "$build_dir/$asset" -C "$build_dir" "$member"
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
    (
        # Keep the outer positional parameters for remote update.
        set -- "$build_dir"/source/*
        [ "$#" -eq 1 ] && [ -f "$1/go.mod" ] || die 'unexpected source archive layout'
        cd "$1"
        CGO_ENABLED=0 GOOS=$(go env GOHOSTOS) GOARCH=$(go env GOHOSTARCH) go build -trimpath \
            -ldflags "-s -w -X github.com/runui/oci-mirror-proxy/internal/cli.version=$version" \
            -o "$build_dir/oci-mirror-proxy" ./cmd/oci-mirror-proxy
    )
fi

run_system mkdir -p "$bin_dir" || die "cannot create $bin_dir; try --prefix \"\$HOME/.local\""
if [ "$manager" = launchd ]; then
    # Back up only installer-owned artifacts; configuration is never rolled back.
    previous_loaded=false
    if launchctl print "system/$service_label" >/dev/null 2>&1; then previous_loaded=true; fi
    [ "$previous_loaded" != true ] || [ -f "$service_file" ] || die 'loaded launchd service has no managed definition; manage it manually'
    previous_disabled=false
    # launchd persists disabled overrides independently of the plist.
    if launchctl print-disabled system | grep -F "\"$service_label\" => true" >/dev/null; then previous_disabled=true; fi
    if [ -e "$destination" ]; then run_system cp -p "$destination" "$build_dir/previous-binary"; fi
    if [ -e "$service_file" ]; then run_system cp -p "$service_file" "$build_dir/previous-service"; fi
    rollback=true
fi
# Rename on the same filesystem so updating a running executable is safe.
staged_binary=$(run_system mktemp "$bin_dir/.oci-mirror-proxy.XXXXXXXX")
run_system install -m 755 "$build_dir/oci-mirror-proxy" "$staged_binary"
run_system mv -f "$staged_binary" "$destination"
staged_binary=
printf 'Installed %s (%s)\n' "$destination" "$version"
if [ -n "$manager" ]; then
    config=/etc/oci-mirror-proxy/config.yaml
    if ! run_system test -e "$config" && ! run_system test -L "$config"; then
        run_system install -d -m 700 /etc/oci-mirror-proxy
        if [ "$manager" = launchd ]; then
            # Desktop forwards host.docker.internal to the Mac's loopback.
            init_config "$config" --admin-socket /var/run/oci-mirror-proxy/admin.sock \
                --listen 127.0.0.1:24817 --advertise host.docker.internal:24817
            printf 'Proxy listens on loopback; Docker Desktop connects via host.docker.internal.\n'
        else
            init_config "$config" --admin-socket /run/oci-mirror-proxy/admin.sock
        fi
    fi
    if [ "$manager" = systemd ]; then
        run_system install -m 644 "$build_dir/service" "$service_file"
        run_system systemctl daemon-reload
        run_system systemctl enable oci-mirror-proxy.service
        run_system systemctl restart oci-mirror-proxy.service
        run_system systemctl is-active --quiet oci-mirror-proxy.service
    elif [ "$manager" = openrc ]; then
        run_system install -m 755 "$build_dir/service" "$service_file"
        run_system rc-update add oci-mirror-proxy default
        if run_system rc-service oci-mirror-proxy status >/dev/null 2>&1; then
            run_system rc-service oci-mirror-proxy restart
        else
            run_system rc-service oci-mirror-proxy start
        fi
        run_system rc-service oci-mirror-proxy status
    else
        # Resolve the same runtime fallback as the daemon, not the installer's
        # inherited user environment, when an older config has no socket path.
        # Set daemon environment only on daemon-facing commands below. Changing
        # the installer's HOME would lose the original Desktop user's identity.
        run_system install -d -m 755 /Library/LaunchDaemons
        run_system install -d -m 700 /var/log/oci-mirror-proxy
        run_system install -o root -g wheel -m 644 "$build_dir/service" "$service_file"
        if launchctl print "system/$service_label" >/dev/null 2>&1; then
            run_system launchctl bootout "system/$service_label"
        fi
        run_system launchctl enable "system/$service_label"
        run_system launchctl bootstrap system "$service_file"
        # Registration alone does not imply a healthy process. The app creates
        # its socket directory on every start, including after /var/run is reset.
        ready=false
        attempts=0
        while [ "$attempts" -lt 15 ]; do
            if launchctl print "system/$service_label" > "$build_dir/launch-state" 2>&1 &&
                grep -E 'pid = [0-9]+' "$build_dir/launch-state" >/dev/null &&
                run_system env -u XDG_CONFIG_HOME -u XDG_CACHE_HOME HOME=/var/root XDG_RUNTIME_DIR=/var/run \
                    "$destination" status --admin-socket "$service_socket" > "$build_dir/status" 2>&1; then
                ready=true
                break
            fi
            attempts=$((attempts + 1))
            sleep 1
        done
        if [ "$ready" != true ]; then
            launchctl print "system/$service_label" >&2 || true
            if [ -f "$build_dir/status" ]; then cat "$build_dir/status" >&2; fi
            die 'launchd service did not become ready; inspect /var/log/oci-mirror-proxy/stderr.log'
        fi
    fi
    # Process-manager state is not proof the admin API is ready. Do not point
    # Docker at an unready proxy, including on systemd and OpenRC installations.
    if [ "$manager" != launchd ]; then
        ready=false
        attempts=0
        while [ "$attempts" -lt 15 ]; do
            if run_system "$destination" status --admin-socket "$service_socket" > "$build_dir/status" 2>&1; then
                ready=true
                break
            fi
            attempts=$((attempts + 1))
            sleep 1
        done
        if [ "$ready" != true ]; then
            cat "$build_dir/status" >&2
            die 'proxy service did not become ready; inspect its service logs'
        fi
    fi
    # A root health check alone cannot verify the caller's out-of-box access.
    if [ "$(id -u)" -ne 0 ]; then
        "$destination" status || die 'service is running but local user access failed'
    elif [ -n "${SUDO_USER:-}" ] && [ "${SUDO_UID:-0}" != 0 ] &&
        [ "$(id -u "$SUDO_USER" 2>/dev/null)" = "${SUDO_UID:-}" ] &&
        [ "$(id -g "$SUDO_USER" 2>/dev/null)" = "${SUDO_GID:-}" ]; then
        sudo -H -u "$SUDO_USER" -- env -u XDG_CONFIG_HOME -u XDG_CACHE_HOME -u XDG_RUNTIME_DIR \
            "$destination" status || die 'service is running but local user access failed'
    fi
    printf 'Service enabled and started (%s). Config: %s\n' "$manager" "$config"
    printf 'Admin commands: oci-mirror-proxy status\n'
    rollback=false
elif [ "$update" = true ]; then
    printf 'Configuration preserved. Restart any running proxy service to use the update.\n'
else
    printf '\nAdd the installation directory to PATH, then run:\n'
    printf '  oci-mirror-proxy serve\n'
fi

printf '\nSyncing remote subscriptions...\n'
if sync_output=$(sync_remote "$@" 2>&1); then
    printf '%s\n' "$sync_output"
else
    printf 'Warning: remote subscription sync failed; installation succeeded.\n%s\n' "$sync_output" >&2
    printf 'If permission is denied, inspect the config directory and its .lock ownership as the intended user. Do not delete active lock files or make private configuration world-writable.\n' >&2
fi

# Docker enrollment is deliberately outside service rollback: an integration
# conflict must not undo an otherwise healthy proxy installation. Existing CLI
# Actual Docker configuration is authoritative on installs and updates.
if [ -n "$manager" ] && [ "$skip_docker" = false ]; then
    printf '\nConfiguring Docker...\n'
    # Keep the outer positional parameters intact: they belong to remote update.
    apply_docker() {
        if [ -n "$docker_identity_error" ]; then
            printf '%s\n' "$docker_identity_error" >&2
            return 1
        fi
        if [ -n "$docker_user" ]; then
            handoff_dir=$(mktemp -d "${TMPDIR:-/tmp}/oci-mirror-proxy-info.XXXXXXXX") || return 1
            server_info="$handoff_dir/server-info.json"
            run_config "$destination" server info --admin-socket "$service_socket" --json > "$server_info" || return 1
            # When invoked as root, keep the directory root-owned and grant only
            # the verified Desktop user read access. No privileged process reads
            # this handoff again; a non-root caller can keep its own ownership.
            if [ "$(id -u)" -eq 0 ]; then chown "$docker_user" "$server_info" || return 1; fi
            chmod 711 "$handoff_dir" || return 1
            chmod 600 "$server_info" || return 1
        fi
        set --
        if [ -n "$docker_user" ]; then
            run_docker_user env -u XDG_CONFIG_HOME -u XDG_CACHE_HOME -u XDG_RUNTIME_DIR HOME="$docker_home" \
                "$destination" dockerd apply --server-info "$server_info" --verify "$@"
        else
            if [ -n "$docker_endpoint" ]; then
                run_system env -u DOCKER_CONTEXT DOCKER_HOST="$docker_endpoint" "$destination" dockerd apply --admin-socket "$service_socket" --verify "$@"
            else
                run_system "$destination" dockerd apply --admin-socket "$service_socket" --verify "$@"
            fi
        fi
    }
    if ! apply_docker; then
        printf 'Warning: proxy installation succeeded, but Docker integration failed. Docker was not restarted. Fix the reported issue, then run:\n' >&2
        printf '  (umask 077; sudo "%s" server info --admin-socket "%s" --json > server-info.json)\n' "$destination" "$service_socket" >&2
        if [ "$os" = linux ]; then
            printf 'For system Docker (select the intended Docker context first):\n' >&2
            printf '  sudo "%s" dockerd apply --server-info server-info.json --verify\n' "$destination" >&2
        else
            printf 'As the Docker Desktop user, without sudo:\n' >&2
            printf '  "%s" dockerd apply --server-info server-info.json --verify\n' "$destination" >&2
        fi
        printf '  rm -f server-info.json\n' >&2
    fi
fi

#!/bin/sh
set -eu

usage() {
    cat <<'EOF'
Install or update oci-mirror-proxy from https://git.zmide.com/bin/oci-mirror-proxy.

Usage: ./install.sh [--prefix DIR] [--version TAG] [--build] [--update] [--service] [--skip-docker]
                    [-- REMOTE_UPDATE_ARGS...]

Options:
  --prefix DIR   Install into DIR/bin (default: /usr/local).
  --version TAG  Install this release tag (default: VERSION environment or latest).
  --build        Compile release source instead of downloading a binary.
  --update       Replace the binary found in PATH, unless --prefix is supplied.
  --service      Install and start a systemd/OpenRC/launchd service (requires root).
  --skip-docker  Do not configure Docker. Binary-only installs always skip Docker.
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
Requires curl, tar and write access to the destination; never invokes sudo.
Configuration is preserved; services are only restarted with --service.
EOF
}

die() {
    printf 'error: %s\n' "$*" >&2
    exit 1
}

run_config() {
    if [ -n "$config_user" ]; then
        sudo -H -u "$config_user" -- env -u XDG_CONFIG_HOME -u XDG_CACHE_HOME -u XDG_RUNTIME_DIR HOME="$config_home" "$@"
    else
        "$@"
    fi
}

init_config() {
    init_path=$1
    shift
    run_config "$destination" init --config "$init_path" "$@" || return 1
    # Only edit the freshly generated docker.io setting, not other registries or race.enabled.
    run_config sh -c '
    adaptive_config=$(mktemp "$(dirname -- "$1")/.config.yaml.XXXXXXXX") || exit 1
    if awk '\''
        /^  [^ ]/ { docker = ($0 == "  docker.io:"); adaptive = 0 }
        /^    [^ ]/ { adaptive = (docker && $0 == "    adaptive:") }
        adaptive && /^      enabled:/ { $0 = "      enabled: true"; updated = 1 }
        { print }
        END { if (!updated) exit 1 }
    '\'' "$1" > "$adaptive_config" && mv -f "$adaptive_config" "$1"; then
        exit 0
    fi
    rm -f "$adaptive_config"
    exit 1
    ' sh "$init_path"
}

sync_remote() {
    [ -z "$sync_skip" ] || { printf '%s\n' "$sync_skip" >&2; return 1; }
    sync_config=
    sync_config_value=false
    for sync_arg do
        if [ "$sync_config_value" = true ]; then
            sync_config=$sync_arg
            sync_config_value=false
            continue
        fi
        case "$sync_arg" in
            --config|-config) sync_config_value=true ;;
            --config=*|-config=*) sync_config=${sync_arg#*=} ;;
        esac
    done
    if [ -n "$sync_config" ]; then
        : # An explicit remote configuration does not change the service configuration.
    elif [ -n "$manager" ]; then
        sync_config=$config
    else
        # Match the CLI's per-user path and user-before-system discovery order.
        case "$os" in
            darwin)
                [ -n "${HOME:-}" ] || return 1
                sync_dir="$HOME/Library/Application Support"
                ;;
            *)
                sync_dir=${XDG_CONFIG_HOME:-${HOME:+$HOME/.config}}
                [ -n "$sync_dir" ] || return 1
                ;;
        esac
        sync_config="$sync_dir/oci-mirror-proxy/config.yaml"
        if [ ! -e "$sync_config" ] && [ ! -L "$sync_config" ]; then
            if [ -e /etc/oci-mirror-proxy/config.yaml ] || [ -L /etc/oci-mirror-proxy/config.yaml ]; then
                sync_config=/etc/oci-mirror-proxy/config.yaml
            fi
        fi
    fi
    if [ ! -e "$sync_config" ] && [ ! -L "$sync_config" ]; then
        init_config "$sync_config" || return 1
    fi
    run_config "$destination" remote update --config "$sync_config" "$@"
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
service=false
skip_docker=false
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
        --service) service=true; shift ;;
        --skip-docker) skip_docker=true; shift ;;
        --) shift; break ;;
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

manager=
config_user=
config_home=
sync_skip=
os=$(uname -s | tr '[:upper:]' '[:lower:]')
# A privileged binary install must not create private root-owned files in the
# invoking user's home. Resolve the account rather than trusting inherited HOME.
if [ "$service" = false ] && [ "$(id -u)" -eq 0 ]; then
    if [ -n "${SUDO_USER:-}${SUDO_UID:-}" ]; then
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
    [ "$(id -u)" -eq 0 ] || die '--service requires root; rerun as root (sudo is not invoked automatically)'
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
        printf 'Warning: no running systemd or OpenRC detected; installing binary only.\n' >&2
        sync_skip='No service manager detected; run remote update as the intended user.'
    fi
fi

# Docker Desktop belongs to the invoking user, not the root service account.
# Resolve that identity before launchd normalizes HOME for the daemon commands.
docker_user=
docker_home=
docker_identity_error=
docker_endpoint=
if [ "$service" = true ] && [ "$os" = linux ] && [ "$skip_docker" = false ] && [ -n "${SUDO_USER:-}" ]; then
    # sudo normally changes HOME, hiding the caller's active Docker context.
    # Resolve it as the caller instead of silently enrolling a different engine.
    if [ "${SUDO_UID:-0}" != 0 ] &&
        [ "$(id -u "$SUDO_USER" 2>/dev/null)" = "${SUDO_UID:-}" ] &&
        [ "$(id -g "$SUDO_USER" 2>/dev/null)" = "${SUDO_GID:-}" ]; then
        if [ -n "${DOCKER_HOST:-}" ] && [ -z "${DOCKER_CONTEXT:-}" ]; then
            docker_endpoint=$DOCKER_HOST
        else
            docker_endpoint=$(sudo -H -u "$SUDO_USER" -- docker context inspect --format '{{.Endpoints.docker.Host}}') ||
                docker_identity_error='cannot inspect the invoking user Docker context'
        fi
        case "$docker_endpoint" in
            unix:///run/user/*) docker_identity_error='automatic system service enrollment does not support rootless Docker; configure it as the Docker user' ;;
        esac
    else
        docker_identity_error='cannot validate the invoking Docker user'
    fi
fi
if [ "$service" = true ] && [ "$os" = darwin ] && [ "$skip_docker" = false ]; then
    if [ -n "${SUDO_USER:-}" ] && [ "${SUDO_UID:-0}" != 0 ] &&
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
    if [ "$rollback" = true ]; then
        printf 'Restoring the previous launchd installation after failure...\n' >&2
        launchctl bootout "system/$service_label" >/dev/null 2>&1 || true
        if [ -f "$build_dir/previous-binary" ]; then
            restore_binary=$(mktemp "$bin_dir/.oci-mirror-proxy.XXXXXXXX") &&
                cp -p "$build_dir/previous-binary" "$restore_binary" &&
                mv -f "$restore_binary" "$destination" || printf 'Warning: binary restore failed.\n' >&2
        else
            rm -f "$destination"
        fi
        if [ -f "$build_dir/previous-service" ]; then
            cp -p "$build_dir/previous-service" "$service_file" || printf 'Warning: service definition restore failed.\n' >&2
            if [ "$previous_loaded" = true ]; then
                launchctl bootstrap system "$service_file" || printf 'Warning: previous service restart failed.\n' >&2
            fi
        else
            rm -f "$service_file"
        fi
        if [ "$previous_disabled" = true ]; then
            launchctl disable "system/$service_label" || printf 'Warning: disabled state restore failed.\n' >&2
        fi
    fi
    rm -rf "$build_dir"
    if [ -n "$staged_binary" ]; then rm -f "$staged_binary"; fi
    if [ -n "${handoff_dir:-}" ]; then rm -rf "$handoff_dir"; fi
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

mkdir -p "$bin_dir" || die "cannot create $bin_dir; try --prefix \"\$HOME/.local\""
if [ "$manager" = launchd ]; then
    # Back up only installer-owned artifacts; configuration is never rolled back.
    previous_loaded=false
    if launchctl print "system/$service_label" >/dev/null 2>&1; then previous_loaded=true; fi
    [ "$previous_loaded" != true ] || [ -f "$service_file" ] || die 'loaded launchd service has no managed definition; manage it manually'
    previous_disabled=false
    # launchd persists disabled overrides independently of the plist.
    if launchctl print-disabled system | grep -F "\"$service_label\" => true" >/dev/null; then previous_disabled=true; fi
    if [ -e "$destination" ]; then cp -p "$destination" "$build_dir/previous-binary"; fi
    if [ -e "$service_file" ]; then cp -p "$service_file" "$build_dir/previous-service"; fi
    rollback=true
fi
# Rename on the same filesystem so updating a running executable is safe.
staged_binary=$(mktemp "$bin_dir/.oci-mirror-proxy.XXXXXXXX")
install -m 755 "$build_dir/oci-mirror-proxy" "$staged_binary"
mv -f "$staged_binary" "$destination"
staged_binary=
printf 'Installed %s (%s)\n' "$destination" "$version"
if [ -n "$manager" ]; then
    config=/etc/oci-mirror-proxy/config.yaml
    if [ ! -e "$config" ] && [ ! -L "$config" ]; then
        install -d -m 700 /etc/oci-mirror-proxy
        if [ "$manager" = launchd ]; then
            init_config "$config" --admin-socket /var/run/oci-mirror-proxy/admin.sock
        else
            init_config "$config" --admin-socket /run/oci-mirror-proxy/admin.sock
        fi
    fi
    if [ "$manager" = systemd ]; then
        install -m 644 "$build_dir/service" "$service_file"
        systemctl daemon-reload
        systemctl enable oci-mirror-proxy.service
        systemctl restart oci-mirror-proxy.service
        systemctl is-active --quiet oci-mirror-proxy.service
    elif [ "$manager" = openrc ]; then
        install -m 755 "$build_dir/service" "$service_file"
        rc-update add oci-mirror-proxy default
        if rc-service oci-mirror-proxy status >/dev/null 2>&1; then
            rc-service oci-mirror-proxy restart
        else
            rc-service oci-mirror-proxy start
        fi
        rc-service oci-mirror-proxy status
    else
        # Resolve the same runtime fallback as the daemon, not the installer's
        # inherited user environment, when an older config has no socket path.
        HOME=/var/root
        XDG_RUNTIME_DIR=/var/run
        export HOME XDG_RUNTIME_DIR
        unset XDG_CONFIG_HOME XDG_CACHE_HOME
        install -d -m 755 /Library/LaunchDaemons
        install -d -m 700 /var/log/oci-mirror-proxy
        install -o root -g wheel -m 644 "$build_dir/service" "$service_file"
        if launchctl print "system/$service_label" >/dev/null 2>&1; then
            launchctl bootout "system/$service_label"
        fi
        launchctl enable "system/$service_label"
        launchctl bootstrap system "$service_file"
        # Registration alone does not imply a healthy process. The app creates
        # its socket directory on every start, including after /var/run is reset.
        ready=false
        attempts=0
        while [ "$attempts" -lt 15 ]; do
            if launchctl print "system/$service_label" > "$build_dir/launch-state" 2>&1 &&
                grep -E 'pid = [0-9]+' "$build_dir/launch-state" >/dev/null &&
                "$destination" status --config "$config" > "$build_dir/status" 2>&1; then
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
            if "$destination" status --config "$config" > "$build_dir/status" 2>&1; then
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
    printf 'Service enabled and started (%s). Config: %s\n' "$manager" "$config"
    printf 'Admin commands: sudo oci-mirror-proxy status --config %s\n' "$config"
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
# state is the authority on updates, so revert is never silently undone.
if [ -n "$manager" ] && [ "$skip_docker" = false ]; then
    printf '\nConfiguring Docker...\n'
    if [ -n "$docker_identity_error" ]; then
        printf 'Proxy installation succeeded; Docker integration failed: %s\n' "$docker_identity_error" >&2
        exit 1
    fi
    if [ -n "$docker_user" ]; then
        handoff_dir=$(mktemp -d "${TMPDIR:-/tmp}/oci-mirror-proxy-info.XXXXXXXX")
        server_info="$handoff_dir/server-info.json"
        "$destination" server info --config "$config" --json > "$server_info" || die 'proxy installed, but could not query server information'
        # Keep the directory root-owned so the reader cannot substitute files.
        # Only the verified user can read the JSON; traversal alone reveals none.
        chown "$docker_user" "$server_info"
        chmod 711 "$handoff_dir"
        chmod 600 "$server_info"
    fi
    # Keep the outer positional parameters intact: they belong to remote update.
    apply_docker() {
        if [ "$update" = true ]; then set -- --if-managed; else set --; fi
        if [ -n "$docker_user" ]; then
            sudo -H -u "$docker_user" -- env -u XDG_CONFIG_HOME -u XDG_CACHE_HOME -u XDG_RUNTIME_DIR HOME="$docker_home" \
                "$destination" dockerd apply --server-info "$server_info" --verify "$@"
        else
            if [ -n "$docker_endpoint" ]; then
                env -u DOCKER_CONTEXT DOCKER_HOST="$docker_endpoint" "$destination" dockerd apply --config "$config" --verify "$@"
            else
                "$destination" dockerd apply --config "$config" --verify "$@"
            fi
        fi
    }
    if ! apply_docker; then
        printf 'Proxy installation succeeded; Docker integration failed. Docker was not restarted. Fix the reported issue, then run as the Docker user (with sudo for system Docker):\n' >&2
        printf '  sudo oci-mirror-proxy server info --config %s --json > server-info.json\n' "$config" >&2
        printf '  oci-mirror-proxy dockerd apply --server-info server-info.json --verify\n' >&2
        exit 1
    fi
fi

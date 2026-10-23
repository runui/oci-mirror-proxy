# Operations

[English](operations.md) | [简体中文](zh-CN/operations.md)

## Commands

Inspect a remote catalog without subscribing using `remote show [--url U] [--region cn]`.
Remote mirror catalogs are managed by `remote list`, `remote update [--url U]
--region cn`, `remote update --all`, and `remote remove --url U`. Updates/removal
support `--dry-run` and previous-result backups; see [remote catalogs](remotes.md).
Upstreams may be added without `--id`; enable/disable/remove accept `--url`
instead of the legacy label. Reload does not interrupt in-flight HTTP transfers.

| Command | Purpose |
|---|---|
| `init [--config F] [--force]` | Write a default configuration file. |
| `serve [--config F] [--admin-socket S] [--log-level L]` | Run the proxy in the foreground. |
| `status [--admin-socket S]` | Show each upstream's breaker state, request and failure counts, and last latency. |
| `reload [--admin-socket S]` | Re-read the configuration file of a running proxy. |
| `upstream list [--registry R]` | List upstreams. |
| `upstream add --id I --url U [--auth T --username N --password-ref REF --first-byte D --overall D --ca-file F --insecure-skip-verify --disabled]` | Add an upstream. |
| `upstream enable\|disable\|remove --id I [--registry R]` | Change an upstream. |
| `dockerd plan [flags]` | Show the files that `apply` would write. Writes nothing. |
| `dockerd apply [flags] [--no-reload]` | Write the dockerd configuration and signal dockerd. |
| `dockerd revert [flags] [--no-reload]` | Remove the selected proxy from current configuration. |
| `doctor [flags]` | Read-only checks: proxy reachable, dockerd reachable, configuration in place. Exits 1 on any failure. |
| `version`, `help` | Self-explanatory. |

Exit codes: `0` success, `1` runtime failure, `2` usage error.

Client commands require a running service and obtain configuration through its admin
API. Select a nonstandard endpoint with `--admin-socket`.
`serve` and `init` support selecting a configuration file.

The private admin API also supports `GET /v1/config/fragments` and
`GET`, `PUT`, `DELETE /v1/config/fragments/{name}` for direct YAML files in the
service's configuration drop-in directory only. File responses contain `name`,
base64-encoded `data`, and a content `revision`; list responses omit contents.
Writes require JSON `revision` (empty for creation) and base64 `data`; deletes
require `revision`. Stale revisions return 412. Files are limited to 1 MiB.
Saving does not reload; use `POST /v1/reload` to apply changes.

### dockerd flags

| Flag | Default (system install) | Default (rootless) |
|---|---|---|
| `--store auto\|containerd\|legacy` | `auto` | `auto` |
| `--rootless` | off | on: per-user paths |
| `--certs-dir` | `/etc/docker/certs.d` | `$XDG_CONFIG_HOME/docker/certs.d` |
| `--daemon-json` | `/etc/docker/daemon.json` | `$XDG_CONFIG_HOME/docker/daemon.json` |
| `--docker-socket` | `/var/run/docker.sock` | `$XDG_RUNTIME_DIR/docker.sock` |
| `--pid-file` | `/var/run/docker.pid` | `$XDG_RUNTIME_DIR/docker.pid` |

`--store auto` asks the running dockerd which image store it uses. Pass
`--store containerd` or `--store legacy` when dockerd is not running.

## Setting up dockerd

### Windows and Docker Desktop

The Windows installer starts the proxy as an automatically started Windows
service. Status queries and the local management API work from a normal PowerShell;
editing protected service files and starting/stopping Windows services require elevation:

```powershell
$proxy = "$env:ProgramFiles\oci-mirror-proxy\bin\oci-mirror-proxy.exe"
$config = "$env:ProgramData\oci-mirror-proxy\config.yaml"
Get-Service oci-mirror-proxy
& $proxy status --admin-socket '\\.\pipe\oci-mirror-proxy-admin'
& $proxy remote update --admin-socket '\\.\pipe\oci-mirror-proxy-admin' --region cn
# After editing config.yaml:
& $proxy reload --admin-socket '\\.\pipe\oci-mirror-proxy-admin'
# After changing listen:
Restart-Service oci-mirror-proxy
# Stop / start the proxy:
Stop-Service oci-mirror-proxy
Start-Service oci-mirror-proxy
```

Service configuration is `%ProgramData%\oci-mirror-proxy\config.yaml` and logs
are in `%ProgramData%\oci-mirror-proxy\logs`. With a custom service name, replace
the service name and pipe name in these commands. To uninstall, run
`.\install.ps1 uninstall` as the Docker user and approve UAC when requested.
It removes this proxy from the current Docker settings before stopping the service;
cleanup failure preserves the installation. `-SkipDocker` explicitly skips cleanup.
Docker is not restarted; configuration and logs are kept. Do not stop the proxy
before cleanup: `dockerd revert` obtains its target from the running service.

Local users can both query and modify the system service through its management
API. Private foreground instances remain restricted to their owning account;
remote pipe connections are rejected. Install the updated binary and restart the
service once to apply a changed pipe ACL.

Each Windows installer run prints a persistent transcript path. User-stage logs
are under `%LOCALAPPDATA%\oci-mirror-proxy-installer\<run-id>\install.log`;
elevated-stage logs are under `%ProgramData%\oci-mirror-proxy-installer\<run-id>\install.log`
and require administrator access. The original window prints the elevated exit
code and log path even if the elevated window closes. Download progress is hidden;
native warnings are plain text and do not by themselves indicate failure.

The system-installed executable is also usable without elevation. Use the full
`$proxy` path above, or open a new terminal and run `oci-mirror-proxy` directly:
the installer adds the program to PATH. Run `init` and
`serve` from a normal PowerShell to use your own configuration; do not pass the
system service's `--config` or admin pipe. If the service is already using port
24817, change `listen` and `advertise` in your user configuration before starting
a second instance. User configuration is stored in
`%APPDATA%\oci-mirror-proxy\config.yaml`. Only service administration needs an
Administrator PowerShell.

For automatic Docker Desktop integration, select the local Linux-container
context and leave `--store` unset. `dockerd plan/apply/revert` merges
`%USERPROFILE%\.docker\daemon.json`, preserves unrelated settings, and stores
backups under `%APPDATA%\oci-mirror-proxy\backups`. Applying does not restart
Docker unless `--restart` is explicitly supplied; alternatively activate the
settings through Docker Desktop. Remote engines, custom `DOCKER_HOST` or
`DOCKER_CONFIG`, and Windows-container Desktop engines are rejected for automatic
local edits.

```powershell
& $proxy dockerd plan
& $proxy dockerd apply
# Restart Docker Desktop manually, or use apply --restart to allow a restart.
& $proxy doctor
# To undo the configuration change:
& $proxy dockerd revert
```

Explicit offline `--store` workflows use paths under `%ProgramData%\docker` and
require manual activation. `--rootless` and `--pid-file` are not supported for
Windows engines.

### Linux Docker Engine

1. Make sure the proxy is running and reachable at the address dockerd will use.
   `doctor` checks this step.
2. Run `dockerd plan`. Read the files it shows. Nothing is written yet.
3. Run `dockerd apply`. Files are written atomically, and any file that already
   existed is backed up under the current user's application directory, under `backups/`.
   Existing configuration is merged; no ownership or installation record is required.
4. dockerd is signalled with `SIGHUP` when the legacy store needs it. The containerd
   store reads `hosts.toml` on every resolution, so no signal is needed.
5. Run `doctor`. It should report every check as passing.

### What is written

**containerd store**: one file per registry, `<certs-dir>/<alias>/hosts.toml`:

```toml
server = "https://registry-1.docker.io"

[host."http://127.0.0.1:24817"]
  capabilities = ["pull", "resolve", "referrers"]
```

containerd tries the `[host]` entries first and the `server` last, so the official
registry remains the fallback. A registry served under a prefix, such as `ghcr.io`,
gets `[host."http://127.0.0.1:24817/ghcr.io"]`.

**Legacy store**: `daemon.json` gets the proxy as the first entry in
`registry-mirrors`. Other entries stay as fallbacks, and all other keys are preserved.
The legacy store supports mirrors for `docker.io` only. `plan`, `apply`, and
`doctor` warn about other registries and skip their dockerd mirror configuration;
the proxy's own routes are unchanged. Docker Hub configuration still proceeds.
If no supported registries are configured, `plan` and `apply` succeed without
writing files or signalling dockerd.

Backups use the current
user's application directory (`~/Library/Application Support/oci-mirror-proxy` on
macOS, `$XDG_CONFIG_HOME/oci-mirror-proxy` or `~/.config/oci-mirror-proxy` on Linux,
and `%APPDATA%\oci-mirror-proxy` on Windows). Installer upgrades perform the
same safe configuration merge as initial setup.

### Reverting

`dockerd revert` removes only the selected proxy entries from
current files, preserving other settings and subsequent edits. It never restores
a historical snapshot or deletes configuration directories. The proxy address is obtained from the selected running service.
Containerd removal also inspects registry directories no longer in subscriptions.
Backups are retained for explicit manual recovery, not configuration detection.

## Reload and restart

| Change | Effect |
|---|---|
| Upstreams, racing, breaker, timeouts, auth (via CLI, admin API, or the file + reload) | Applied immediately. Unchanged upstreams keep their breaker state and counters. |
| `listen` | Logged; takes effect after a restart. |
| dockerd configuration (`dockerd apply`) | Containerd store: immediate for new pulls. Legacy store: dockerd is signalled. |

A reload that fails validation keeps the previous configuration running and logs
the reason.

## Rootless dockerd

The proxy binds to the address in `listen`. Rootless dockerd runs in its own
network namespace, and RootlessKit blocks connections to the host's loopback
address by default (`DOCKERD_ROOTLESS_ROOTLESSKIT_DISABLE_HOST_LOOPBACK`, see
`contrib/dockerd-rootless.sh` in the moby tree). The project does not work around
this. Choose one of these:

- Keep the default and point `advertise` at an address that rootless dockerd can
  reach, and make sure `listen` is bound to it.
- Set `DOCKERD_ROOTLESS_ROOTLESSKIT_DISABLE_HOST_LOOPBACK=false` in the dockerd
  service override. This lets rootless dockerd reach host loopback services, which
  is a security trade-off you must accept consciously.

The default rootless paths are `$XDG_CONFIG_HOME/docker/daemon.json` and
`$XDG_RUNTIME_DIR/docker.pid`. Pass `--daemon-json` or `--pid-file` if your
installation uses different paths.

## Logs

Logs go to stderr in `logfmt` format. Levels:

- `DEBUG`: every served request, with status and elapsed time.
- `INFO`: startup, reloads, shutdown, and content misses (`content not found on any
  upstream`). A content miss is normal: dockerd falls back to the official registry.
- `WARN`: request failures where upstreams could not serve the request.
- `ERROR`: reload failures and rollback failures.

## Troubleshooting

| Symptom | Likely cause and what to do |
|---|---|
| `dockerd` not connecting to the proxy | Run `doctor`. Check `advertise`, the `listen` address and the firewall. Remember that rootless dockerd cannot reach host loopback by default. |
| Every pull is slow by about 5 seconds | A silent upstream is first in the list. Check `status`. Once its breaker opens, the cost disappears. Lower `timeouts.first_byte`, or enable racing. |
| An upstream is skipped | Its breaker is open. `status` shows the state. It is probed again after `breaker.cooldown`. |
| Pulls fall back to Docker Hub | The proxy answered 404 or 502. Check `status` and the `WARN` lines. Missing images legitimately fall back. |
| `reload failed` in the log | The configuration file does not validate. The message names the problem. The running configuration is unchanged. |
| `secret ... not set or empty` | The environment variable behind a `password` reference is missing in the proxy's environment. |
| `cannot detect the image store` | dockerd is not reachable at `--docker-socket`. Start it, or pass `--store`. |

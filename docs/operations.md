# Operations

[English](operations.md) | [简体中文](zh-CN/operations.md)

## Commands

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
| `dockerd apply [flags] [--force] [--no-reload]` | Write the dockerd configuration and signal dockerd. |
| `dockerd revert [flags] [--no-reload]` | Undo the last apply. |
| `doctor [flags]` | Read-only checks: proxy reachable, dockerd reachable, configuration in place. Exits 1 on any failure. |
| `version`, `help` | Self-explanatory. |

Exit codes: `0` success, `1` runtime failure, `2` usage error.

### dockerd flags

| Flag | Default (system install) | Default (rootless) |
|---|---|---|
| `--store auto\|containerd\|legacy` | `auto` | `auto` |
| `--rootless` | off | on: per-user paths |
| `--certs-dir` | `/etc/docker/certs.d` | `$XDG_CONFIG_HOME/docker/certs.d` |
| `--daemon-json` | `/etc/docker/daemon.json` | `$XDG_CONFIG_HOME/docker/daemon.json` |
| `--docker-socket` | `/var/run/docker.sock` | `$XDG_RUNTIME_DIR/docker.sock` |
| `--pid-file` | `/var/run/docker.pid` | `$XDG_RUNTIME_DIR/docker.pid` (assumed, see below) |
| `--state-dir` | `/var/lib/oci-mirror-proxy` | `$XDG_STATE_HOME/oci-mirror-proxy` |

`--store auto` asks the running dockerd which image store it uses. Pass
`--store containerd` or `--store legacy` when dockerd is not running.

## Setting up dockerd

1. Make sure the proxy is running and reachable at the address dockerd will use.
   `doctor` checks this step.
2. Run `dockerd plan`. Read the files it shows. Nothing is written yet.
3. Run `dockerd apply`. Files are written atomically, and any file that already
   existed is backed up under `<state-dir>/backups/`. A file that this tool did not
   create is refused unless you pass `--force`.
4. dockerd is signalled with `SIGHUP` when the legacy store needs it. The containerd
   store reads `hosts.toml` on every resolution, so no signal is needed.
5. Run `doctor`. It should report every check as passing.

### What is written

**containerd store**: one file per registry, `<certs-dir>/<alias>/hosts.toml`:

```toml
# Managed by oci-mirror-proxy. Changes are overwritten by `oci-mirror-proxy dockerd apply`.
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
writing files, changing existing apply state, or signalling dockerd.

### Reverting

`dockerd revert` restores each file from its backup, or removes it when it did not
exist before. It also removes directories that apply created, when they are empty,
and deletes the state file. Backups are kept for inspection.

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

The rootless `daemon.json` location (`$XDG_CONFIG_HOME/docker/daemon.json`) and the
rootless pid file location (`$XDG_RUNTIME_DIR/docker.pid`) are assumptions. The first
was checked against the moby source. Pass `--pid-file` if your installation differs.

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
| `file exists and was not created by oci-mirror-proxy` | Another tool owns that file. Read it, then rerun with `--force` if you want to take over. A backup is made first. |
| `cannot detect the image store` | dockerd is not reachable at `--docker-socket`. Start it, or pass `--store`. |

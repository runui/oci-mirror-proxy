# oci-mirror-proxy

[English](README.md) | [简体中文](README.zh-CN.md)

A local, read-only registry mirror proxy for dockerd. It sits between dockerd and
one or more user-chosen Registry v2 mirrors and adds what dockerd's built-in
mirror support lacks:

- **Runtime management.** Add, remove and disable upstreams without restarting dockerd.
- **Failure isolation.** A silent or failing mirror costs one timeout, then a
  circuit breaker takes it out of rotation until a probe succeeds.
- **Optional racing.** Hedged requests can answer from whichever mirror responds first.
- **Resumable layers.** A layer that breaks mid-transfer continues from the same
  byte offset on another mirror, and the daemon still verifies the digest.
- **Safe fallback.** When the proxy cannot serve a request, dockerd falls back to
  the official registry, so the proxy can never make a pull impossible.

It supports both dockerd image stores: containerd (`hosts.toml`) and the classic
graphdriver store (`daemon.json`, docker.io only).

## Status

Implemented and tested: the registry API for pulls, failover, circuit breaking,
hedged requests, blob resumption, token and basic authentication, the admin socket,
configuration reload, and dockerd setup with revert.

Not implemented (by design, for now): push, a local blob cache (the extension
point exists in `internal/cache`), keychain secret backends, and metrics export.

## Requirements

- Go 1.26 or newer, to build.
- Linux with dockerd for real use. Rootless dockerd needs the listen address
  reachable from its network namespace, which is left to the operator (see
  [docs/operations.md](docs/operations.md#rootless-dockerd)).

## Installation

Install the latest release without cloning the repository:

```sh
curl -fsSL https://git.zmide.com/bin/oci-mirror-proxy/raw/branch/master/install.sh | sh -s -- --prefix "$HOME/.local"
export PATH="$HOME/.local/bin:$PATH"
oci-mirror-proxy version
```

Update an existing installation:

```sh
curl -fsSL https://git.zmide.com/bin/oci-mirror-proxy/raw/branch/master/install.sh | sh -s -- --update --prefix "$HOME/.local"
```

On Linux, install and start a system service with root privileges:

```sh
curl -fsSL https://git.zmide.com/bin/oci-mirror-proxy/raw/branch/master/install.sh | sudo sh -s -- --service
# Update the system installation and restart its service:
curl -fsSL https://git.zmide.com/bin/oci-mirror-proxy/raw/branch/master/install.sh | sudo sh -s -- --update --prefix /usr/local --service
```

The script downloads the matching Linux `amd64` or `arm64` binary and verifies
its SHA256 checksum. When no matching binary is available, or `--build` is
specified, it builds release source using Go 1.26.8 or newer (or Go toolchain
auto-download). Use `--version v1.0.1` to select a release.
To use a mirror, set `DOWNLOAD_BASE_URL` to its repository root URL (with the
same `/releases` and `/archive` layout). When piping the script, pass the variable
to `sh`, not just to `curl`:

```sh
curl -fsSL https://git.zmide.com/bin/oci-mirror-proxy/raw/branch/master/install.sh | DOWNLOAD_BASE_URL=https://mirror.example.com/bin/oci-mirror-proxy sh -s -- --prefix "$HOME/.local"
```

The default prefix is `/usr/local`, which requires write permission. Existing
configuration is preserved; without `--service`, no configuration is initialized
and no service is installed or restarted. Docker is never modified or restarted.
Restart a running proxy manually after updating without `--service`.

## Quick start

```sh
make build                                   # produces dist/oci-mirror-proxy

dist/oci-mirror-proxy init                   # writes the per-user config file
dist/oci-mirror-proxy serve &                # runs the proxy in the background

dist/oci-mirror-proxy upstream add \
  --id mirror-a --url https://mirror-a.example.com

sudo dist/oci-mirror-proxy dockerd plan      # shows what will change; writes nothing
sudo dist/oci-mirror-proxy dockerd apply     # writes the dockerd configuration
dist/oci-mirror-proxy doctor                 # verifies proxy, dockerd and config
```

Start from [examples/config.example.yaml](examples/config.example.yaml) for a
complete configuration with every option explained.

## Documentation

- [docs/configuration.md](docs/configuration.md): every configuration key and its default.
- [docs/operations.md](docs/operations.md): commands, dockerd integration, reload, troubleshooting.
- [docs/architecture.md](docs/architecture.md): components, request flow and failure handling.
- [docs/testing.md](docs/testing.md): test layers, manual smoke test, and what is not verified.

## Security notes

- The registry API has no authentication, so it listens on `127.0.0.1` by default.
  A non-loopback `listen` exposes unauthenticated pulls to the network.
- Credentials never appear in the configuration file: a password is a reference
  such as `env:NAME` or `file:/path`. Secrets are redacted in logs and in `%v` output.
- Client `Authorization` headers are never forwarded upstream.
- The admin socket is protected by file permissions: its directory is `0700`, and
  the socket is `0600`.
- Files that dockerd apply writes are backed up first, and revert restores them.
  Files that this tool did not create are not overwritten without `--force`.

## Project policy

The project depends only on the standard library and `gopkg.in/yaml.v3`. It must not
depend on moby, docker/docker or containerd modules. `make deps-check` enforces this.

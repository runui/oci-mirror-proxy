# oci-mirror-proxy

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

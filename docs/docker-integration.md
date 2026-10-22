# Automatic Docker integration

[English](docker-integration.md) | [简体中文](zh-CN/docker-integration.md)

The same commands configure a local Docker Engine or Docker Desktop on macOS:

```sh
oci-mirror-proxy dockerd plan --config config/config.yaml
oci-mirror-proxy dockerd apply --config config/config.yaml
oci-mirror-proxy doctor --config config/config.yaml
oci-mirror-proxy dockerd revert
```

The active Docker connection selects the integration automatically. Remote SSH
and TCP connections are rejected before local configuration is modified. Docker
Desktop is identified from the daemon, not the selected image store. Unknown
environments fail explicitly rather than guessing configuration paths.

## Deployment and activation

Use the repository's standard Compose file and `examples/config.docker.yaml` on
both platforms. It publishes the registry on host loopback using bridge networking.
Mount the whole writable configuration directory; the non-root process saves
updates using atomic rename. Other containers on the same network may still reach
the unauthenticated registry, so do not share its network with untrusted workloads.

On macOS, run the host CLI as your normal user, not with `sudo`. Keep `docker` in
`PATH`. The integration merges the proxy into `~/.docker/daemon.json`, preserves
other settings and mirrors, and backs up the original before writing. The proxy
becomes the first mirror. Only Docker Hub is configured; other registries are
reported as unsupported by this integration.

When activation requires a restart, ordinary `apply` saves the configuration and
reports activation as pending. Explicitly authorize interruption with:

```sh
oci-mirror-proxy dockerd apply --config config/config.yaml --restart
```

Docker Desktop's CLI must support `docker desktop restart`. If activation fails,
the error distinguishes saved configuration from activation failure. Backups remain
available. `doctor` checks both the configured file and the daemon's loaded mirror
list; a successful API check alone does not prove a pull went through the proxy.

On Linux, existing store-specific configuration, rootless options, `--force`, and
`--no-reload` remain available. Explicit file and store overrides also support
offline configuration generation; ordinary use does not require them. Linux host
configuration may require elevated permissions.

## Recovery

```sh
oci-mirror-proxy dockerd revert --restart
```

Use `--restart` only when activation requires it. Revert restores original content
and permissions, or removes a file created by the tool. Backups are retained.
Managed files edited externally are not overwritten, even with `--force`; resolve
the conflict before retrying. Operations use an exclusive state lock. After a
crash, remove a stale lock only after confirming no integration operation is running.

Revert before removing the proxy deployment. Restarting Docker may regenerate
internal network identifiers and logs; restoring configuration is not a VM disk
snapshot rollback.

## Verified scope

Docker Desktop 4.95.0 with Engine 29.8.2 on Apple Silicon was verified with the
containerd image store: automatic configuration, restart, ordinary Docker Hub
image-name pulls through a controlled upstream (including a real image layer),
diagnostics, repeated apply, and exact configuration restoration. This does not
establish support for every Desktop release, alternative macOS engines, BuildKit
mirrors, or every public upstream. No automatic Desktop upgrade is performed.

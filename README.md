# oci-mirror-proxy

[English](README.md) | [简体中文](README.zh-CN.md)

A local, read-only registry mirror proxy for dockerd. Manage upstreams at runtime,
isolate failing mirrors with circuit breakers, race optional hedged requests, and
resume interrupted layers on another mirror. Docker can still fall back to the
official registry.

Supports Linux Docker Engine and macOS/Windows Docker Desktop (Linux containers),
with both containerd and classic graphdriver image stores. No push or local blob cache.

## Quick start

Install the latest release without cloning the repository:

```sh
curl -fsSL https://ocimp.zmide.com/install.sh | sh
oci-mirror-proxy version
```

Installs to `/usr/local/bin` by default, requesting sudo if needed.

Or install with Homebrew on macOS or Linux (Intel/Apple Silicon/ARM64):

```sh
brew tap runui/oci-mirror-proxy https://github.com/runui/oci-mirror-proxy.git
brew install runui/oci-mirror-proxy/oci-mirror-proxy
```

See [Homebrew setup and services](apps/docs/content/docs/installation.mdx#homebrew)
for first-run configuration, Docker integration, and updates.

On Linux, install a system service and automatically connect local Docker:

```sh
curl -fsSL https://ocimp.zmide.com/install.sh | sudo sh -s -- --service
```

See [installation](apps/docs/content/docs/installation.mdx) for Windows, macOS
services, updates, uninstall, and building from source (Go 1.26 or newer).
For container deployment, see [Docker Compose](apps/docs/content/docs/docker.mdx).
The registry API has no authentication and listens on loopback by default; review
the [security notes](apps/docs/content/docs/index.mdx#security-notes) before exposing it.

## Preview

oci-mirror-proxy completed in **508.0 s** versus **740.3 s** directly from the
mirror: **1.46× as fast**, or **31.4% less time** in this preview.

| Direct mirror · 740.3 s | Through oci-mirror-proxy · 508.0 s |
| :---: | :---: |
| <img src="docs/demos/direct-mirror.gif" alt="Direct mirror pull" width="552" height="363"> | <img src="docs/demos/oci-mirror-proxy.gif" alt="Proxy pull" width="552" height="363"> |
| [asciinema recording](docs/demos/direct-mirror.cast) | [asciinema recording](docs/demos/oci-mirror-proxy.cast) |

GIFs are time-compressed previews; the original recordings preserve real timing.

## Documentation

Read the [documentation website](https://ocimp.zmide.com/docs/). It lives in [apps/docs](apps/docs/README.md), independently
of the proxy. Its authoritative English and Chinese content is in
[`apps/docs/content/docs`](apps/docs/content/docs).

- [Overview and requirements](apps/docs/content/docs/index.mdx)
- [Installation](apps/docs/content/docs/installation.mdx)
- [Automatic Docker integration](apps/docs/content/docs/docker-integration.mdx)
- [Remote mirror catalogs](apps/docs/content/docs/remotes.mdx) and [catalog details](subscriptions/README.md)
- [Configuration reference](apps/docs/content/docs/configuration.mdx)
- [Operations and troubleshooting](apps/docs/content/docs/operations.mdx)
- [Architecture](apps/docs/content/docs/architecture.mdx)
- [Testing](apps/docs/content/docs/testing.mdx)

Start from [examples/config.example.yaml](examples/config.example.yaml) for a
complete configuration with every option explained.

# Docker deployment

[English](docker.md) | [简体中文](zh-CN/docker.md)

## Requirements and image

Use Linux Docker Engine with the Compose plugin. The supplied Compose file uses
host networking so the host's dockerd can reach `127.0.0.1:24817`. Docker Desktop
and rootless networking need additional network configuration; they are not the
default deployment target. See [rootless dockerd](operations.md#rootless-dockerd).

Images are available from the project's Gitea container registry:

```text
git.zmide.com/bin/oci-mirror-proxy:latest
git.zmide.com/bin/oci-mirror-proxy:v1.0.1
git.zmide.com/bin/oci-mirror-proxy:dev
```

Images support `linux/amd64` and `linux/arm64`. `latest` tracks stable `v*` tags,
version tags also include prereleases, and `dev` tracks `master`. Use a version
tag or digest in production. For a private package, run
`docker login git.zmide.com` first.

The multi-stage Dockerfile builds a static Go binary and uses a `scratch` runtime
with system CA certificates, no shell, and UID/GID `65532:65532`.

## Start with Compose

From the repository root:

```sh
mkdir -p config
cp examples/config.docker.yaml config/config.yaml
# The non-root container must be able to atomically replace the config file.
sudo chown -R 65532:65532 config
sudo chmod 700 config
sudo chmod 600 config/config.yaml
docker compose pull
docker compose up -d --no-build
docker compose ps
docker compose logs -f
```

To select another image, put the following in `.env` (or export the variable):

```dotenv
OCI_MIRROR_PROXY_IMAGE=git.zmide.com/bin/oci-mirror-proxy:v1.0.1
```

Alternatively, build locally, without pulling a published application image:

```sh
OCI_MIRROR_PROXY_IMAGE=oci-mirror-proxy:local docker compose build
OCI_MIRROR_PROXY_IMAGE=oci-mirror-proxy:local docker compose up -d --no-build
```

The initial configuration intentionally has no upstream mirrors. Add a real
mirror before using the proxy; an empty list causes dockerd to fall back to the
official registry:

```sh
docker compose exec oci-mirror-proxy oci-mirror-proxy upstream add \
  --id mirror-a --url https://mirror-a.example.com
docker compose exec oci-mirror-proxy oci-mirror-proxy status
curl -fsS http://127.0.0.1:24817/v2/
```

Mount the **whole configuration directory**, not just `config.yaml`: upstream
management saves via atomic rename and needs write access to the directory.
Changes survive container recreation. For manual edits, use `sudoedit
config/config.yaml` and then:

```sh
docker compose exec oci-mirror-proxy oci-mirror-proxy reload
```

The health check queries the local admin socket. It checks process responsiveness,
not upstream availability or host dockerd configuration. The socket lives in a
private tmpfs and is not exposed to the host. Logs go to stderr; SIGTERM performs
a graceful shutdown. No privileged mode, Docker socket mount, or host Docker
configuration mount is needed.

## Configure the host's dockerd

Starting the container does **not** configure Docker to use it. Install the CLI
on the host using the [installation instructions](../README.md#installation),
then use the same configuration to preview and apply the host integration:

```sh
sudo oci-mirror-proxy dockerd plan --config "$PWD/config/config.yaml"
sudo oci-mirror-proxy dockerd apply --config "$PWD/config/config.yaml"
sudo oci-mirror-proxy doctor --config "$PWD/config/config.yaml"
docker pull hello-world
```

The host CLI does not need the container's private admin socket for these commands.
Do not start a second proxy service on the host; if using the install script, omit
`--service`. For manual configuration or rollback, see
[operations](operations.md#configure-dockerd).

The default listener is loopback-only because the registry API has no
authentication. Changing it to `0.0.0.0` with host networking exposes it to the
network. For bridge networking, override `network_mode`, publish the port only on
host loopback, change the container listener to `0.0.0.0:24817`, and keep
`advertise` set to an address reachable by dockerd; do not just add `ports` to a
host-network service.

For authenticated mirrors, explicitly pass referenced environment variables via
Compose `environment` (a `.env` entry alone does not inject them into the
container), or mount Compose secrets and use `file:/run/secrets/...`. Mount custom
CA files read-only and set `tls.ca_file` to their container path. Ensure mounted
files are readable by UID 65532. Never bake secrets into the image/build context.

## Update and stop

```sh
docker compose pull
docker compose up -d --no-build
docker compose down
```

`down` leaves `config/` intact. If removing the proxy permanently, first run
`sudo oci-mirror-proxy dockerd revert` on the host to restore its previous Docker
configuration.

## Local builds

Run tests and build a local image:

```sh
docker build --target test .
docker build -t oci-mirror-proxy:local --build-arg VERSION=dev .
```

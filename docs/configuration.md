# Configuration reference

[English](configuration.md) | [简体中文](zh-CN/configuration.md)

The configuration file is YAML. Unknown keys are rejected, so a misspelt option
fails at startup instead of being silently ignored. The file can be written
by hand, or changed through the CLI, which validates every change before saving.

Durations use Go syntax: `500ms`, `5s`, `1m30s`.

## Drop-in files and URL merging

`config.yaml` also loads direct, visible `config.d/*.yaml` and `*.yml` files.
`--config /path/proxy.yml` uses `/path/proxy.d/`. The main file is required;
a missing drop-in directory is fine. Files load lexically after the main file,
except `local-overrides.yaml`, which is reserved for existing-URL patches and
loads last. Hidden/temp/backup files and subdirectories are ignored.

Mappings merge recursively; later explicitly declared values override earlier
ones, including `false`. Defaults are applied only after merging. Upstreams
merge by normalized URL within each registry: first occurrence determines
position, later explicit options override the same URL. `upstreams: []` does not
clear previous sources. Scheme/host case, default ports, and a root slash are
normalized; meaningful paths remain distinct. No upstream ID is required.
Duplicate keys, multiple YAML documents, YAML aliases/merge keys, unknown fields,
and unsupported explicit versions are rejected. A bad fragment rejects the
entire update, keeping the running configuration unchanged.

`local-overrides.yaml` patches only already-provided URLs, so local disabled
choices survive updates without reintroducing URLs removed from all sources.
See [remote catalogs](remotes.md) for subscription files and backups.

## Top level

| Key | Default | Description |
|---|---|---|
| `version` | `1` | Schema version. Only `1` is accepted. |
| `log_level` | `info` | Optional logging level: `debug`, `info`, `warn`, or `error`. Reloadable; an explicit `serve --log-level` overrides the file. Omitted from generated configuration by default. |
| `listen` | `127.0.0.1:24817` | Address the registry API listens on. |
| `advertise` | (derived) | `host:port` that dockerd uses to reach the proxy. Required in practice when `listen` is a wildcard address. Must name a concrete host. |
| `admin.socket` | per-user default | Path of the admin socket. The default is `$XDG_RUNTIME_DIR/oci-mirror-proxy/admin.sock`, or the user cache directory when `XDG_RUNTIME_DIR` is unset. |
| `registries` | `docker.io` | Map from registry alias to its settings. |

The address dockerd connects to is `advertise` when set. Otherwise it is
`listen`, with wildcard hosts (`0.0.0.0`, `::`) mapped to `127.0.0.1`.

For debugging, add top-level `log_level: debug` and run `oci-mirror-proxy reload`.
Debug logs correlate requests with `request_id` and record routing strategy,
candidates, attempt latency/status, breaker skips, hedging and the selected upstream.
`upstream` is the legacy label when present, otherwise its URL; `via` is its configured base URL. `upstream hit`
means a successful response; `upstream selected` identifies the actual winner
(other successful racing responses may be discarded). This does not indicate
the mirror's internal cache status. Latency ends at response headers, not blob
completion. Resumed attempts include `range`. Attempt debug logs do not include
authentication headers or signed redirect URLs.

## Registries

Each key under `registries` is a registry alias:

- `docker.io` is served at the root path, `/v2/`, which is where dockerd looks.
- Any other alias, such as `ghcr.io`, is served under a path prefix, `/ghcr.io/v2/`.
  The alias must be a host name without a port. `v2` is reserved.

| Key | Default | Description |
|---|---|---|
| `origin` | `https://registry-1.docker.io` for `docker.io`, otherwise `https://<alias>` | Official endpoint. dockerd falls back to it when the proxy cannot serve a request. |
| `race.enabled` | `false` | Enable hedged requests for this registry. |
| `race.delay` | `500ms` | With racing on, start another upstream if nothing has answered within this time. Must be positive. |
| `race.max_parallel` | `2` | Maximum concurrent attempts per request. Must be at least 2 when racing is on. |
| `breaker.threshold` | `3` | Consecutive failures before an upstream is skipped. |
| `breaker.cooldown` | `30s` | How long a skipped upstream stays out of rotation before one probe request is tried. |
| `upstreams` | `[]` | Ordered list of upstreams. Earlier entries are tried first. |

Racing sends extra requests to the upstreams. Docker Hub enforces pull rate limits,
so racing is off by default and should be enabled deliberately.

## Upstreams

| Key | Default | Description |
|---|---|---|
| `id` | omitted | Optional legacy label, unique within the registry. 1 to 63 letters, digits, `-` or `_`. URL is the identity; new configurations need no ID. |
| `url` | required | Base URL of a Registry v2 endpoint. `http` or `https`, with no query, fragment or credentials. A base path such as `/hub` is allowed. |
| `disabled` | `false` | Keep the upstream in the file but out of rotation. |
| `timeouts.connect` | `10s` | Maximum time to establish the connection: the TCP dial and the TLS handshake. Raise it for slow or proxied paths. |
| `timeouts.first_byte` | `5s` | Maximum wait for response headers. This is the cost of meeting a silent upstream. |
| `timeouts.overall` | `60s` | Bound for metadata requests: manifests, tag lists and referrers. Blob transfers are not bounded by it. |
| `auth.type` | `none` | `none`, `basic` or `token`. See below. |
| `auth.username` | | Username for `basic`, or for `token` when the token service requires one. |
| `auth.password` | | Secret reference, for example `env:MIRROR_PASSWORD` or `file:/run/secrets/mirror`. Literal passwords are rejected. |
| `tls.ca_file` | | Extra CA certificate file for this upstream. It is added to the system roots. |
| `tls.insecure_skip_verify` | `false` | Disable certificate verification for this upstream. Avoid this except for testing. |

### Authentication types

- **`none`**: requests are sent without credentials.
- **`basic`**: `username` and `password` are sent as HTTP Basic credentials with every request.
- **`token`**: the upstream answers with a `WWW-Authenticate: Bearer` challenge. The proxy requests
  a token from the realm named in the challenge, caches it per scope until shortly before it expires,
  and retries the request once. Set `username` and `password` only when the token service requires them.

Credentials are scoped to their upstream. They are never sent to another upstream,
and the client's own `Authorization` header is never forwarded.

### Secret references

`password` accepts a reference, not a value:

| Reference | Resolves to |
|---|---|
| `env:NAME` | The value of environment variable `NAME`. It must be set and non-empty. |
| `file:/path` | The contents of the file, with trailing line breaks removed. |

References are resolved when the upstream is built, so a missing variable is
reported at startup or on reload, not when the first request arrives.

## Timing defaults in practice

With the defaults, a silent upstream first in the list costs about 5 seconds per
request until its breaker opens. After three such failures the upstream is skipped
for 30 seconds, then probed once. With racing enabled and the defaults, a silent
upstream costs about 500 milliseconds, because the next upstream is started
after that delay.

## Changing the configuration

- **Through the CLI or admin API**: changes are validated, saved, and applied
  without a restart. Validation failures leave the files and running configuration
  unchanged. New upstreams go in the main file; enable/disable choices are saved
  in `local-overrides.yaml`.
- **By hand**: edit the file, then run `oci-mirror-proxy reload` or send `SIGHUP`
  to the process. A file that fails validation is rejected, and the running
  configuration is kept.
- **Listen address**: a change to `listen` is logged, and takes effect after a restart.
- **In-flight transfers**: reload does not interrupt existing HTTP requests or
  change their resume candidates. Later HTTP requests, including those in the
  same `docker pull`, use the new configuration.

# Remote mirror catalogs

[English](remotes.md) | [简体中文](zh-CN/remotes.md)

Subscribe to a JSON mirror catalog to select mirrors by region and save them
to local configuration files.

```sh
oci-mirror-proxy init
oci-mirror-proxy remote update --url https://github.com/runui/oci-mirror-proxy/raw/refs/heads/master/subscriptions/mirrors.json --region cn
oci-mirror-proxy remote list
oci-mirror-proxy remote update --url https://example.com/mirrors.json --region cn,apac
oci-mirror-proxy remote update --all --dry-run
oci-mirror-proxy remote update --all
oci-mirror-proxy remote remove --url https://example.com/mirrors.json
```

`update` adds a source on first use. The command above uses the catalog from the
[GitHub repository](https://github.com/runui/oci-mirror-proxy):

```text
https://github.com/runui/oci-mirror-proxy/raw/refs/heads/master/subscriptions/mirrors.json
```

Use `--url` to select a catalog; omitting both `--url` and `--all` selects the
CLI's built-in default.

The first update requires `--region`. Later updates reuse that source's saved
regions, or replace them when `--region` is supplied. Regions are exact labels;
multiple labels select their union. `global` is not included automatically.
`--all` updates configured sources with their respective saved selections and
cannot be combined with `--url` or `--region`.

All commands accept `--config`. Update/remove accept `--dry-run`, `--no-reload`,
and `--admin-socket`. A dry run previews changes without saving them.
An empty selection is rejected unless `update --allow-empty` is explicit.
`remove` always requires `--url`. There is no background updater.

## Files, atomic replacement, and backup

For `/etc/oci-mirror-proxy/config.yaml`, each source owns a file in `config.d/`:

```text
config.d/example.com@0123456789abcdef.yaml
config.d/example.com@0123456789abcdef.yaml.bak
```

Each source has a separate file, even when two URLs share a hostname. Keep the
header comments containing the source URL and regions intact so the CLI can
recognize the subscription.

Protect these files if a source URL contains a private query token. CLI output
redacts queries; generated files and backups have mode `0600`.

Updates are validated with the rest of the configuration and saved atomically.
The previous file is backed up to `.yaml.bak`; if the backup fails, the current
file is not changed. First creation has no previous backup; unchanged updates
rewrite neither current nor backup. Removing a source also creates a backup.
Backups are not loaded as configuration. Restore manually by copying `.bak` to
its original `.yaml` path, then run `reload`.

With `--all`, successful sources remain updated when another source fails;
any failure exits with `1`. Reload is attempted after processing the sources.
If the proxy is stopped, files take effect at startup.
If reload is rejected or times out, the CLI reports that files are saved but
runtime application is unconfirmed, and exits with `1`. Saved files are kept;
run `oci-mirror-proxy reload` or repeat the update to retry reloading.

Reload does not interrupt existing HTTP transfers or change their resume
candidates. Later HTTP requests, including those in the same `docker pull`,
use the new configuration.

## Local choices

Upstreams merge by normalized URL **within each registry**. Removing one remote
does not remove an endpoint still provided by another source. See
[configuration merging](configuration.md#drop-in-files-and-url-merging).

```sh
oci-mirror-proxy upstream disable --url https://docker.example.com
oci-mirror-proxy upstream enable --url https://docker.example.com
```

These choices are saved in `config.d/local-overrides.yaml`, loaded last and
applied only to existing URLs. They survive source updates without resurrecting
URLs whose last source has disappeared. Custom timeout/auth/TLS options can also
be placed there. Avoid editing generated source fields: the next update replaces
them. `upstream remove --url` removes current definitions, but a later catalog
update may reintroduce the URL; disable it for a persistent local choice.

## Catalog format and trust

The repository's [catalog](../subscriptions/mirrors.json) and
[JSON Schema](../subscriptions/schema.json) define version 1. Mirrors have a
display name, multiple region labels, and explicit per-registry endpoint URLs.
No mirror ID is required. Unknown fields, duplicate JSON keys, unsupported
versions, invalid regions, and invalid URLs are rejected. Generated endpoints
use anonymous `auth: {type: token}` to support Bearer challenges, with no secrets.

Downloads have a 30-second timeout, a 4 MiB decoded-body limit, and bounded
redirects. HTTPS certificates are checked and HTTPS-to-HTTP redirects rejected.
An explicitly supplied HTTP source warns. Standard proxy environment variables
are supported. Catalogs cannot set credentials, local paths, TLS bypasses, or
global proxy settings.

A catalog is a trust input: digest verification protects addressed content, not
the authenticity/freshness of mutable tag resolution. Region labels are not an
uptime guarantee. Review the [catalog details](../subscriptions/README.md),
provider limits, and local reachability before relying on a public mirror.

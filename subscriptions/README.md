# Mirror catalog

[English](README.md) | [简体中文](README.zh-CN.md)

[`mirrors.json`](mirrors.json) is the repository's mirror catalog.
[`schema.json`](schema.json) describes its JSON format and provides editor hints.
For subscription commands, see [remote catalogs](../apps/docs/content/docs/remotes.mdx).

## Format

- `version`: catalog format version, currently `1`.
- `regions`: a map of region labels to display names.
- `mirrors`: mirror entries with a `name`, optional `website`, region labels in
  `regions`, and per-registry URLs in `endpoints`. One mirror can support multiple
  regions.

## Contribute a mirror

Help other users discover public mirrors: add a mirror, correct its regions or
URLs, document provider restrictions, or report an endpoint that has stopped
working. Mirror operators are welcome to contribute their own public services.
If you prefer not to edit JSON, [open an issue](https://github.com/runui/oci-mirror-proxy/issues/new)
with the operator's website, registry URLs, applicable regions, and any usage limits.

### Fork and submit a pull request

1. [Fork the repository](https://github.com/runui/oci-mirror-proxy/fork) into your
   GitHub account and create a branch such as `add-example-mirror`.
2. Edit `subscriptions/mirrors.json` in your fork. Append an entry to `mirrors`,
   or update the existing entry for the same operator. The GitHub file editor
   works too; a local development environment is not required.
3. Commit the change and [open a pull request](https://github.com/runui/oci-mirror-proxy/compare)
   from your branch to `runui/oci-mirror-proxy:master`. Include the official endpoint
   documentation and explain the regions and restrictions. If reporting a
   reachability problem, include the affected image, region, time, and error;
   never include credentials or private tokens.

Append an entry like this inside `mirrors` (the example domains are placeholders):

```json
{
  "name": "Example Public Mirror",
  "website": "https://mirror.example.com/docs",
  "regions": ["cn", "apac"],
  "endpoints": [
    {"registry": "docker.io", "url": "https://docker.mirror.example.com"},
    {"registry": "ghcr.io", "url": "https://ghcr.mirror.example.com"}
  ]
}
```

Use region labels declared in the top-level `regions` map; add a label and its
display name there if needed. Select the regions the service is intended to
support, not just the location of its server. Use public Registry v2 base URLs,
not web portals or individual image URLs. Link the operator's official documentation,
and describe allowlists, rate limits, authentication requirements, or tag-cache
behavior in this README. Do not add passwords, query tokens, or private endpoints.

### Format and validate JSON locally

Local formatting tools require Node.js 20.19+ (20.x) or 22.12+ and npm. From the
repository root, run:

```sh
npm ci
npm run format
npm run format:check
npm run validate
npm run check
```

Formatting commands only target `subscriptions/**/*.json`. `npm run validate`
validates `mirrors.json` against `schema.json`; `npm run check` runs both formatting
and Schema checks. `npm ci` also installs a Git pre-commit hook that checks staged
subscription JSON files with Oxfmt and validates the staged catalog against the
staged Schema whenever subscription JSON changes, including Schema changes.
The hook does not fix or stage files automatically. If a check fails, correct the
reported errors (use `npm run format` for formatting), review and stage the
corrected files, then commit again.
Node.js is not needed to build or run the Go application.

### Use your fork as a catalog

You can use your own catalog without waiting for a PR to be merged. Commit and
push `mirrors.json`, then use its raw URL. Replace `YOUR-USER` and `YOUR-BRANCH`
with your GitHub account and branch name:

```sh
oci-mirror-proxy remote update \
  --url https://github.com/YOUR-USER/oci-mirror-proxy/raw/refs/heads/YOUR-BRANCH/subscriptions/mirrors.json \
  --region cn --dry-run

oci-mirror-proxy remote update \
  --url https://github.com/YOUR-USER/oci-mirror-proxy/raw/refs/heads/YOUR-BRANCH/subscriptions/mirrors.json \
  --region cn
```

Choose a region present in your catalog. Each URL is a separate subscription:
adding your fork does not replace an existing source. If you want to use only
your fork, run `remote list`, then remove the old source with
`remote remove --url OLD-CATALOG-URL`. Keep your fork up to date with GitHub's
**Sync fork** before making further changes.

## Available mirrors

### DaoCloud Public Image Mirror

- Official documentation: https://github.com/DaoCloud/public-image-mirror
- Registry endpoints: `docker.m.daocloud.io`,
  `ghcr.m.daocloud.io`, `gcr.m.daocloud.io`, `k8s.m.daocloud.io`, and `quay.m.daocloud.io`.
- The provider documents allowlists, rate limits, congestion and stale mutable-tag
  responses while synchronization runs. Prefer pinned digests. Public image
  coverage is not unlimited; read the linked policy before use.
- Regions: `cn`, `apac`, `eu`, `na`.

### ZimaOS Image Mirror

- Provider website: https://www.zimaos.com
- Docker Hub, GCR, Quay and GHCR endpoints, respectively: `storeproxy.zimaos.com`,
  `gcr.zimaos.com`, `quay.zimaos.com`, `ghcr.zimaos.com`.
- Regions: `cn`, `apac`, `eu`, `na`.

### 1ms Image Mirror

- Provider website: https://1ms.run
- Docker Hub, GCR, Quay and GHCR endpoints, respectively: `docker.1ms.run`,
  `gcr.1ms.run`, `quay.1ms.run`, `ghcr.1ms.run`.
- Regions: `cn`, `apac`, `eu`, `na`.

### 1Panel Docker Hub Mirror

- Provider website: https://1panel.cn
- Registry endpoint: `docker.1panel.live`.
- Region: `cn`. Access is restricted to mainland China.

## Project contributors

Thank you to everyone improving the project and its mirror catalog!

[![Project contributors](https://contrib.rocks/image?repo=runui/oci-mirror-proxy)](https://github.com/runui/oci-mirror-proxy/graphs/contributors)

[Catalog history](https://github.com/runui/oci-mirror-proxy/commits/master/subscriptions/mirrors.json)

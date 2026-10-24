# Documentation website

The independent Fumadocs application for oci-mirror-proxy. It exports static HTML,
client-side search, Markdown, LLM indexes, and Open Graph images. The Go proxy
does not serve or depend on this application.

## Local development

Use Node.js **22.22 or newer** (Node 24 LTS recommended) and npm. From this directory:

```sh
npm ci
npm run dev
```

Open <http://localhost:3000>. English documentation is at `/docs/`; Chinese is at
`/zh-CN/docs/`. Language switching preserves the current document. Pages without
a Chinese translation display a notice and the English text, use an English
canonical URL, and are excluded from the sitemap and search-engine indexing.

The home entry URL (`/`) uses a saved language choice, or otherwise the browser's
preferred languages: Chinese opens `/zh-CN/`, English stays at `/`, and unmatched
languages fall back to English. Direct document links keep their URL language.
Manual language switching preserves the document, query string, and anchor, and
remembers the choice in browser storage when available.

The theme follows the system appearance until a user selects light or dark mode.
The header's theme menu also offers a system option to restore automatic switching.
Theme choices persist in browser storage; the not-found page uses the same theme.

```sh
npm run types:check
npm test
npm run build
npm run check:links
npm start -- --listen tcp://127.0.0.1:3001
```

`npm start` serves `out/` as ordinary static files, not a Next.js server. It does
not use an SPA fallback. Test direct visits and refreshes on deep links here.
The link checker validates exported local links and anchors, localized SEO and
social metadata, JSON-LD, robots, sitemap coverage and language alternates,
translation fallback metadata, search, Markdown, LLM outputs, OG images,
repository assets (compact JSON for subscriptions, byte-for-byte copies otherwise),
and Cloudflare's per-file size limit. It does not
make external network requests.

## Editing content

- Edit `content/docs/*.mdx`. English uses `name.mdx`; Chinese uses
  `name.zh-CN.mdx`. Keep titles and descriptions in frontmatter and begin body
  headings at level two. Navigation order is in the localized `meta*.json` files.
- Use `/docs/slug` or `/zh-CN/docs/slug` for website links. Repository files and
  subscription catalog documentation use absolute GitHub links.
- English is the default language. Add translated files only when translations
  are available; the English fallback is generated automatically.
- Installers, subscriptions, and demo recordings remain in the repository root.
  The `predev` and `prebuild` scripts copy `install.sh`, `install.ps1`, all files
  under `subscriptions/`, and the four display assets from `docs/demos/` into
  ignored locations in `public/`. Subscription JSON files are minified without
  changing their data; all other assets are copied byte-for-byte. Next.js includes
  these prepared files in `out/`. Source files keep their original formatting.
  Build from a full repository checkout, not a standalone copy of this directory.

The official static Next.js/Fumadocs MDX scaffold provides the application setup.
`lib/source.ts` defines the MDX collection with the installed macro API. Generated
`.source/`, `.next/`, and `out/` files are not content sources and are not committed.
This directory has its own `package.json` and lockfile; no root npm workspace is
required. UI fonts are bundled locally. The OG image renderer may fetch fallback
fonts at build time for characters outside its bundled font.

## SEO and discovery

Every build generates `/sitemap.xml` and `/robots.txt` as static files. The sitemap
is derived from the MDX source and contains both homepages and documentation with
actual translations. Adding or removing a document updates it on the next build;
there is no separate URL list to maintain. Sitemap URLs and HTML canonical links
share the trailing-slash format. Both include reciprocal `en` / `zh-CN` language
alternates when translations exist and use English for `x-default`. No build-time
timestamp is presented as a content modification date.

Homepages have localized titles and descriptions. Documentation uses its
frontmatter title and description, with Open Graph and Twitter cards pointing to
the generated 1200 × 630 images. Homepages include WebSite, SoftwareApplication,
and WebPage JSON-LD; documentation includes TechArticle and BreadcrumbList JSON-LD.
Metadata and structured data are rendered in the exported HTML without requiring
client-side JavaScript.

English fallback pages use the original English canonical URL and `noindex`, with
no translation alternates or duplicate JSON-LD. Not-found pages are also `noindex`.
On Cloudflare Pages, `_headers` sets `X-Robots-Tag: noindex` for search data,
Markdown/LLM exports, installers, and catalogs, leaving the HTML documentation as
the search results. `robots.txt` allows crawling so search engines can read these
directives; public downloads and LLM access remain available. On other hosts,
apply the equivalent response headers from `public/_headers`.

After deployment, the sitemap can be submitted in Google Search Console or Bing
Webmaster Tools as `https://ocimp.zmide.com/sitemap.xml`. Search engines also discover
it through `robots.txt`; submission and verification are not required for a build.

## Cloudflare Pages

Create a **Pages** project using Git integration with this repository:

| Setting | Value |
| --- | --- |
| Production branch | `master` |
| Root directory | `apps/docs` |
| Framework preset | None |
| Build command | `npm run build` |
| Build output directory | `out` |
| Node version (`NODE_VERSION`) | `24` |
| `SITE_URL` | `https://ocimp.zmide.com` (default; optional override) |

Canonical URLs, the sitemap, robots, structured data, and social images use
`https://ocimp.zmide.com` by default, including on local and Pages preview builds. Set `SITE_URL` to an
absolute URL only when a different metadata origin is needed. Installation
commands always fetch scripts from the production domain. No credentials or
Cloudflare API token are needed to build locally.

If restricting Pages build watches, include `apps/docs/**`, `docs/demos/**`,
`install.sh`, `install.ps1`, and `subscriptions/**`. The build needs the full
checkout for repository assets. GitHub Actions watches these paths, validates the
documentation, and uploads the static export; it does not deploy or configure Pages.

This is a static export, **not** a Workers/OpenNext deployment. No adapter,
middleware, server functions, or runtime rewrites are needed. Keep `/docs/` and
`/zh-CN/docs/` unchanged. The exported `404.html` gives Pages a real not-found page
instead of its implicit SPA fallback. `public/_headers` supplies content types for
search, text exports, installers, and subscription files, with revalidation for
installers and catalogs. No domain or Pages project is provisioned by the repository.

## Published installers and catalogs

The static export publishes these URLs directly, without a backend:

- `https://ocimp.zmide.com/install.sh`
- `https://ocimp.zmide.com/install.ps1`
- `https://ocimp.zmide.com/subscriptions/mirrors.json`
- `https://ocimp.zmide.com/subscriptions/schema.json`

The rest of `subscriptions/`, including its README files and any subdirectories,
is published at the same relative paths. Edit the original repository files, not
their generated copies. A new site build is required to publish changes.

The site hosts scripts and catalogs, not release binaries or source archives.
The installation commands use the installer's built-in download source for
release downloads and checksum verification. The documentation domain has no
`/releases` or `/archive` endpoints. The CLI's built-in subscription URL is unchanged; use
`--url https://ocimp.zmide.com/subscriptions/mirrors.json` to select the published catalog.

## Other static hosts

Publish the complete `out/` directory at the origin root. Enable directory indexes
(`.../index.html`), serve the extensionless `/api/search` file as JSON, and return
`404.html` with an HTTP 404 for unknown paths. Do not rewrite every URL to
`index.html`. Text and image exports are real files:

| Output | Location |
| --- | --- |
| English LLM index / full text | `/llms.txt`, `/llms-full.txt` |
| Chinese LLM index / full text | `/zh-CN/llms.txt`, `/zh-CN/llms-full.txt` |
| Page Markdown | `/llms.mdx/docs/{en\|zh-CN}/{slug}/content.md` |
| Page OG image | `/og/docs/{en\|zh-CN}/{slug}/image.png` |
| Search database | `/api/search` |
| Sitemap | `/sitemap.xml` |
| Crawler rules | `/robots.txt` |
| Installers | `/install.sh`, `/install.ps1` |
| Mirror catalogs and related files | `/subscriptions/*` |

For the overview, omit `{slug}/`. Chinese LLM and Markdown exports mark English
fallback content explicitly.

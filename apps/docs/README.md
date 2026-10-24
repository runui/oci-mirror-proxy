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

```sh
npm run types:check
npm test
npm run build
npm run check:links
npm start -- --listen tcp://127.0.0.1:3001
```

`npm start` serves `out/` as ordinary static files, not a Next.js server. It does
not use an SPA fallback. Test direct visits and refreshes on deep links here.
The link checker validates exported local links and anchors, language metadata,
translation fallback metadata, search, Markdown, LLM outputs, OG images, and
Cloudflare's per-file size limit. It does not make external network requests.

## Editing content

- Edit `content/docs/*.mdx`. English uses `name.mdx`; Chinese uses
  `name.zh-CN.mdx`. Keep titles and descriptions in frontmatter and begin body
  headings at level two. Navigation order is in the localized `meta*.json` files.
- Use `/docs/slug` or `/zh-CN/docs/slug` for website links. Repository files and
  subscription catalog documentation use absolute GitHub links.
- English is the default language. Add translated files only when translations
  are available; the English fallback is generated automatically.
- Demo recordings remain in the repository's `docs/demos/`. The `predev` and
  `prebuild` scripts copy the four display assets into ignored `public/demos/`.
  Build from a full repository checkout, not a standalone copy of this directory.

The official static Next.js/Fumadocs MDX scaffold provides the application setup.
`lib/source.ts` defines the MDX collection with the installed macro API. Generated
`.source/`, `.next/`, and `out/` files are not content sources and are not committed.
This directory has its own `package.json` and lockfile; no root npm workspace is
required. UI fonts are bundled locally. The OG image renderer may fetch fallback
fonts at build time for characters outside its bundled font.

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
| `SITE_URL` | The public production origin, e.g. `https://docs.example.com` |

Set `SITE_URL` for production builds before publishing so canonical URLs, the
sitemap, and social images use the public domain. Set a separate preview value
if desired. Without `SITE_URL`, the build uses Cloudflare's `CF_PAGES_URL`, or
`http://localhost:3000` locally. Values must be absolute URLs. No credentials or
Cloudflare API token are needed to build locally.

Optionally restrict Pages build watches to `apps/docs/**` and `docs/demos/**` to
avoid rebuilds for Go-only changes. The build must still have access to the full
checkout for demo assets. GitHub Actions validates documentation separately and
uploads the static export for inspection; it does not deploy or configure Pages.

This is a static export, **not** a Workers/OpenNext deployment. No adapter,
middleware, server functions, or runtime rewrites are needed. Keep `/docs/` and
`/zh-CN/docs/` unchanged. The exported `404.html` gives Pages a real not-found page
instead of its implicit SPA fallback. `public/_headers` supplies content types for
search and text exports on Pages. No domain or Pages project is provisioned by
the repository.

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

For the overview, omit `{slug}/`. Chinese LLM and Markdown exports mark English
fallback content explicitly.

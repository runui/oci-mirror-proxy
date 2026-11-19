# Documentation performance

## Local cold-load measurements (2026-11-19)

Production static export served at `http://localhost:4173`, measured in Chrome
using the existing tab. Each recorded load used Performance Trace with
`reload: true` and `ignoreCache: true`. Mobile conditions: 390 x 844 at DPR 3,
Slow 4G, CPU slowdown 4x. Three samples per page; values below are milliseconds.

| Chinese page | LCP samples | Median LCP | CLS, each sample |
| --- | --- | --- | --- |
| Home | 895, 1080, 852 | 895 | 0 |
| Installation | 848, 854, 852 | 852 | 0 |
| Configuration | 857, 859, 867 | 859 | 0 |

TTFB was approximately 563 ms, except the first homepage sample (600 ms).
These are local lab results, not production or field Core Web Vitals.
The cache bypass applies to recorded reloads; navigation stress tests below
are separate functional tests, not cold-load timing comparisons.

Before the font change, configuration-page CLS was reproducibly 0.0322.
Layout Shift entries pointed to text in the drop-in configuration paragraph;
the shift coincided with the Latin body font finishing at approximately 2.3 s.
Using `font-display: optional` eliminated that shift in three recorded loads.
On a slow first visit, the fallback font may remain for the entire visit.

An earlier single homepage sample before CSS inlining had LCP 1427 ms.
It is not a repeated paired baseline and must not be used to claim a precise
percentage improvement.

## Navigation checks

With Slow 4G and CPU slowdown 4x, two animated WebP downloads were explicitly
started with `cache: 'no-store'` before clicking the header Docs link.
Both were still pending at click time:

| Header | Click to document request start | Result |
| --- | --- | --- |
| Desktop | 12.4 ms | `/zh-CN/docs/`, overview heading displayed |
| Mobile menu | 11.4 ms | `/zh-CN/docs/`, overview heading displayed |

A separate parser-time check clicked the desktop Docs anchor while
`document.readyState` was `loading` and the element had no React props.
The overview document loaded successfully before homepage hydration.
This validates independence from image completion and client routing; it
does not establish the root cause of the previously intermittent failure.

## Safeguards and remaining checks

- Content and installation commands are exported in HTML.
- CSS is inlined to avoid a separate cold render-blocking stylesheet request.
- Header destinations use native anchors and canonical trailing slashes.
- Demo images remain animated, sized, lazy, asynchronously decoded, and low priority.
- Fonts include Latin and Cyrillic subsets without late swapping.
- `npm test` checks navigation and font source invariants; `npm run check:links`
  checks exported content, assets, links, and render-blocking stylesheet absence.

Deploy before evaluating actual CDN TTFB, compression, and response headers.
Repeat all-language mobile checks and longer navigation stress runs when changing
layout, routing, fonts, or the hosting platform. Lab click samples are not field INP.

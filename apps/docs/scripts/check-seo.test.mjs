import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { inspectHtml } from './check-export.mjs';
import { checkPageSeo, checkRobots, checkSeoHeaders, checkSitemap } from './check-seo.mjs';

const siteUrl = 'https://docs.example.test';
const en = `${siteUrl}/docs/install/`;
const zh = `${siteUrl}/zh-CN/docs/install/`;
const languages = { en, 'zh-CN': zh, 'x-default': en };
const options = { path: '/docs/install/', siteUrl, languages };

function pageHtml({ fallback = false, home = false, locale = 'en', alternates = languages } = {}) {
	const prefix = locale === 'en' ? '' : `/${locale}`;
	const url = home ? `${siteUrl}${prefix}/` : `${siteUrl}${prefix}/docs/install/`;
	const title = 'Installation · oci-mirror-proxy';
	const description = 'Install and run the proxy.';
	const image = `${siteUrl}/og/docs/en/install/image.png`;
	const graph = home
		? [{ '@type': 'WebSite' }, { '@type': 'SoftwareApplication' }, { '@type': 'WebPage', url, description, inLanguage: locale }]
		: [
				{ '@type': 'TechArticle', url, description, inLanguage: locale },
				{ '@type': 'BreadcrumbList', itemListElement: [{ position: 1, name: 'Installation', item: url }] },
			];
	const meta = {
		description,
		robots: fallback ? 'noindex, follow' : 'index, follow',
		'og:type': 'website',
		'og:site_name': 'oci-mirror-proxy',
		'og:url': url,
		'og:title': title,
		'og:description': description,
		'og:locale': locale === 'ru' ? 'ru_RU' : 'en_US',
		...(!fallback ? { 'og:locale:alternate': 'zh_CN' } : {}),
		'og:image': image,
		'og:image:width': '1200',
		'og:image:height': '630',
		'og:image:alt': title,
		'twitter:card': 'summary_large_image',
		'twitter:title': title,
		'twitter:description': description,
		'twitter:image': image,
	};
	return `<html lang="${locale}"><head><title>${title}</title>
    ${Object.entries(meta)
		.map(([name, content]) => `<meta ${name.startsWith('og:') ? 'property' : 'name'}="${name}" content="${content}">`)
		.join('')}
    <link rel="canonical" href="${url}">
    ${
		fallback
			? ''
			: Object.entries(alternates)
					.map(([language, href]) => `<link rel="alternate" hreflang="${language}" href="${href}">`)
					.join('')
	}
    </head><body>${fallback ? '' : `<script type="application/ld+json">${JSON.stringify({ '@context': 'https://schema.org', '@graph': graph })}</script>`}</body></html>`;
}

function sitemapXml(entries) {
	return `<?xml version="1.0" encoding="UTF-8"?>
    <urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">
    ${entries
		.map(
			({ url, languages }) =>
				`<url><loc>${url}</loc>${Object.entries(languages)
					.map(([language, href]) => `<xhtml:link rel="alternate" hreflang="${language}" href="${href}" />`)
					.join('')}</url>`,
		)
		.join('')}
    </urlset>`;
}

test('validates canonical, localized social metadata, and structured page data', () => {
	const html = pageHtml();
	assert.deepEqual(checkPageSeo(inspectHtml(html), options), []);
	const broken = html
		.replace(`rel="canonical" href="${en}"`, `rel="canonical" href="${en.slice(0, -1)}"`)
		.replace('content="en_US"', 'content="zh_CN"')
		.replace('hreflang="zh-CN"', 'hreflang="zh"')
		.replace('"inLanguage":"en"', '"inLanguage":"zh-CN"');
	const errors = checkPageSeo(inspectHtml(broken), options);
	for (const message of ['canonical must be', 'og:locale', 'language alternates', 'structured data must match']) assert(errors.some((error) => error.includes(message)));
});

test('requires homepage schema types and HTML-safe JSON-LD', () => {
	const homeOptions = { ...options, path: '/' };
	const html = pageHtml({ home: true });
	assert.deepEqual(checkPageSeo(inspectHtml(html), homeOptions), []);
	assert(checkPageSeo(inspectHtml(html.replace('SoftwareApplication', 'Product')), homeOptions).some((error) => error.includes('missing SoftwareApplication')));
	assert(checkPageSeo(inspectHtml(html.replace('"@graph":', '"unsafe":"<tag>","@graph":')), homeOptions).some((error) => error.includes('escape HTML')));
	assert(checkPageSeo(inspectHtml(html.replace('"@graph":', 'invalid-json:')), homeOptions).some((error) => error.includes('invalid JSON-LD')));
});

test('validates Russian document and homepage metadata, alternates, and English fallback', () => {
	for (const home of [false, true]) {
		const path = home ? '/ru/' : '/ru/docs/install/';
		const russianUrl = `${siteUrl}${path}`;
		const russianLanguages = { ru: russianUrl, 'zh-CN': home ? `${siteUrl}/zh-CN/` : zh, 'x-default': home ? `${siteUrl}/` : en };
		const html = pageHtml({ home, locale: 'ru', alternates: russianLanguages });
		assert.deepEqual(checkPageSeo(inspectHtml(html), { path, siteUrl, languages: russianLanguages }), []);
		assert(
			checkPageSeo(inspectHtml(html.replace('content="ru_RU"', 'content="zh_CN"')), { path, siteUrl, languages: russianLanguages }).some((error) =>
				error.includes('og:locale'),
			),
		);
	}
	assert.deepEqual(checkPageSeo(inspectHtml(pageHtml({ fallback: true })), { path: '/ru/docs/install/', siteUrl, fallbackPath: '/docs/install/' }), []);
});

test('fallback and not-found pages stay noindex without alternate or duplicate structured data', () => {
	const fallbackOptions = { path: '/zh-CN/docs/install/', siteUrl, fallbackPath: '/docs/install/' };
	const html = pageHtml({ fallback: true });
	assert.deepEqual(checkPageSeo(inspectHtml(html), fallbackOptions), []);
	assert(checkPageSeo(inspectHtml(html.replace('noindex, follow', 'index, follow')), fallbackOptions).some((error) => error.includes('expected noindex')));
	const notFound = '<title>Not found</title><meta name="description" content="Page not found"><meta name="robots" content="noindex, follow">';
	assert.deepEqual(checkPageSeo(inspectHtml(notFound), { path: '/404.html', siteUrl }), []);
	assert(checkPageSeo(inspectHtml(`${notFound}<link rel="canonical" href="${en}">`), { path: '/404.html', siteUrl }).some((error) => error.includes('404 must not')));
});

test('sitemap covers exactly the canonical pages with reciprocal language alternates', () => {
	const entries = [
		{ url: en, languages },
		{ url: zh, languages },
	];
	const xml = sitemapXml(entries);
	assert.deepEqual(checkSitemap(xml, entries), []);
	assert(checkSitemap(xml, [...entries, { url: `${siteUrl}/docs/new/`, languages }]).some((error) => error.includes('Missing sitemap URL')));
	assert(checkSitemap(xml, entries.slice(0, 1)).some((error) => error.includes('Unexpected sitemap URL')));
	assert(checkSitemap(sitemapXml([...entries, entries[0]]), entries).some((error) => error.includes('Duplicate sitemap URL')));
	assert(checkSitemap(xml.replace('hreflang="zh-CN"', 'hreflang="zh"'), entries).some((error) => error.includes('Incorrect sitemap language alternates')));
	assert(checkSitemap(xml.replace(`<loc>${en}</loc>`, `<loc>${en.slice(0, -1)}</loc>`), entries).some((error) => error.includes('Unexpected sitemap URL')));
	const englishOnly = { url: en, languages: { en, 'x-default': en } };
	assert.deepEqual(checkSitemap(sitemapXml([englishOnly]), [englishOnly]), []);
});

test('robots allows crawlers to see noindex pages and points to the configured sitemap', () => {
	const text = `User-Agent: *\nAllow: /\n\nSitemap: ${siteUrl}/sitemap.xml\n`;
	assert.deepEqual(checkRobots(text, siteUrl), []);
	assert(checkRobots(`${text}Disallow: /zh-CN/\n`, siteUrl).some((error) => error.includes('allow crawling')));
	assert(checkRobots(text, 'https://other.example.test').some((error) => error.includes('canonical sitemap')));
});

test('CDN headers exclude non-HTML exports without blocking documentation indexing', async () => {
	const text = await readFile(new URL('../public/_headers', import.meta.url), 'utf8');
	assert.deepEqual(checkSeoHeaders(text), []);
	assert(checkSeoHeaders(text.replace('X-Robots-Tag: noindex', '')).some((error) => error.includes('Missing noindex')));
	assert(checkSeoHeaders(`${text}\n/*\n  X-Robots-Tag: noindex\n`).some((error) => error.includes('Unexpected noindex')));
});

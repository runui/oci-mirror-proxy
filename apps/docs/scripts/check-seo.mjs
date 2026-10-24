import { parseFragment } from 'parse5';
import { locales } from '../lib/locales.ts';

const socialLocales = { en: 'en_US', 'zh-CN': 'zh_CN', ru: 'ru_RU' };

function sameEntries(actual, expected) {
	return JSON.stringify(Object.entries(actual).sort()) === JSON.stringify(Object.entries(expected).sort());
}

export function checkPageSeo(document, { path, siteUrl, languages = {}, fallbackPath }) {
	const errors = [];
	const fail = (message) => errors.push(`${path}: ${message}`);
	const elements = document.elements;
	const metas = (name) => elements.filter(({ tag, attrs }) => tag === 'meta' && (attrs.name === name || attrs.property === name));
	const meta = (name) => metas(name)[0]?.attrs.content;
	const links = (rel) => elements.filter(({ tag, attrs }) => tag === 'link' && attrs.rel === rel);
	const title = elements.find(({ tag }) => tag === 'title')?.text;
	const description = meta('description');
	const notFound = ['/404.html', '/404/', '/_not-found/'].includes(path);
	const noindex = notFound || Boolean(fallbackPath);
	const url = new URL(fallbackPath ?? path, siteUrl).href;
	const locale = fallbackPath ? 'en' : (locales.find((locale) => path.startsWith(`/${locale}/`)) ?? 'en');

	if (elements.filter(({ tag }) => tag === 'title').length !== 1 || !title?.trim()) fail('expected one nonempty SEO title');
	if (metas('description').length !== 1 || !description?.trim()) fail('expected one nonempty SEO description');
	if (metas('robots').some(({ attrs }) => /\bnoindex\b/i.test(attrs.content)) !== noindex) fail(`expected ${noindex ? 'noindex' : 'indexable'} robots metadata`);
	if (notFound) {
		if (links('canonical').length || links('alternate').some(({ attrs }) => attrs.hreflang)) fail('404 must not have canonical or language alternates');
		if (elements.some(({ tag, attrs }) => tag === 'script' && attrs.type === 'application/ld+json')) fail('404 must not have structured data');
		return errors;
	}

	if (links('canonical').length !== 1 || links('canonical')[0]?.attrs.href !== url) fail(`canonical must be ${url}`);
	const alternates = links('alternate').filter(({ attrs }) => attrs.hreflang);
	if (alternates.length !== Object.keys(languages).length || !sameEntries(Object.fromEntries(alternates.map(({ attrs }) => [attrs.hreflang, attrs.href])), languages))
		fail('incorrect language alternates');

	const expectedMeta = {
		'og:type': 'website',
		'og:site_name': 'oci-mirror-proxy',
		'og:url': url,
		'og:title': title,
		'og:description': description,
		'og:locale': socialLocales[locale],
		'og:image:width': '1200',
		'og:image:height': '630',
		'og:image:alt': title,
		'twitter:card': 'summary_large_image',
		'twitter:title': title,
		'twitter:description': description,
	};
	for (const [name, value] of Object.entries(expectedMeta)) {
		if (metas(name).length !== 1 || !value || meta(name) !== value) fail(`incorrect ${name}`);
	}
	const socialAlternates = metas('og:locale:alternate')
		.map(({ attrs }) => attrs.content)
		.sort();
	const expectedSocialAlternates = Object.keys(languages)
		.filter((language) => language !== locale && language !== 'x-default')
		.map((language) => socialLocales[language])
		.sort();
	if (JSON.stringify(socialAlternates) !== JSON.stringify(expectedSocialAlternates)) fail('incorrect Open Graph language alternates');
	try {
		const image = new URL(meta('og:image'));
		if (image.origin !== new URL(siteUrl).origin || !image.pathname.endsWith('/image.png') || meta('twitter:image') !== image.href) fail('incorrect social image URL');
	} catch {
		fail('missing or invalid social image URL');
	}

	const scripts = elements.filter(({ tag, attrs }) => tag === 'script' && attrs.type === 'application/ld+json');
	if (fallbackPath) {
		if (scripts.length) fail('fallback must not publish duplicate structured data');
		return errors;
	}
	if (scripts.length !== 1) fail('expected one JSON-LD script');
	for (const script of scripts) {
		try {
			const data = JSON.parse(script.text);
			if (data['@context'] !== 'https://schema.org' || !Array.isArray(data['@graph'])) throw new Error('missing schema.org graph');
			const graph = data['@graph'];
			const home = path === '/' || locales.some((locale) => path === `/${locale}/`);
			for (const type of home ? ['WebSite', 'SoftwareApplication', 'WebPage'] : ['TechArticle', 'BreadcrumbList']) {
				if (!graph.some((item) => item['@type'] === type)) fail(`missing ${type} structured data`);
			}
			const page = graph.find((item) => item['@type'] === (home ? 'WebPage' : 'TechArticle'));
			if (page?.url !== url || page?.inLanguage !== locale || page?.description !== description) fail('structured data must match the canonical page and language');
			if (!home) {
				const crumbs = graph.find((item) => item['@type'] === 'BreadcrumbList')?.itemListElement;
				if (
					!crumbs?.length ||
					crumbs.at(-1).item !== url ||
					crumbs.some((crumb, index) => crumb.position !== index + 1 || !crumb.name || new URL(crumb.item).origin !== new URL(siteUrl).origin)
				)
					fail('invalid structured breadcrumbs');
			}
			if (script.text.includes('<')) fail('JSON-LD must escape HTML characters');
		} catch {
			fail('invalid JSON-LD');
		}
	}
	return errors;
}

export function checkSitemap(xml, entries) {
	const errors = [];
	if (!xml.includes('xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"') || !xml.includes('xmlns:xhtml="http://www.w3.org/1999/xhtml"'))
		errors.push('Sitemap must declare sitemap and XHTML namespaces');
	const expected = new Map(entries.map((entry) => [entry.url, entry.languages]));
	const seen = new Set();
	for (const match of xml.matchAll(/<url>([\s\S]*?)<\/url>/g)) {
		const url = /<loc>(.*?)<\/loc>/.exec(match[1])?.[1];
		if (seen.has(url)) errors.push(`Duplicate sitemap URL: ${url}`);
		seen.add(url);
		if (!expected.has(url)) {
			errors.push(`Unexpected sitemap URL: ${url}`);
			continue;
		}
		const alternates = [...match[1].matchAll(/<xhtml:link\b([^>]*?)\s*\/>/g)].map((link) =>
			Object.fromEntries(parseFragment(`<link ${link[1]}>`).childNodes[0].attrs.map(({ name, value }) => [name, value])),
		);
		if (
			alternates.some((link) => link.rel !== 'alternate') ||
			alternates.length !== Object.keys(expected.get(url)).length ||
			!sameEntries(Object.fromEntries(alternates.map((link) => [link.hreflang, link.href])), expected.get(url))
		)
			errors.push(`Incorrect sitemap language alternates: ${url}`);
	}
	for (const url of expected.keys()) if (!seen.has(url)) errors.push(`Missing sitemap URL: ${url}`);
	return errors;
}

export function checkRobots(text, siteUrl) {
	const errors = [];
	if (!/^User-Agent: \*\s*$/im.test(text) || !/^Allow: \/\s*$/im.test(text) || /^Disallow:\s*\S/im.test(text))
		errors.push('robots.txt must allow crawling, including pages with noindex directives');
	const sitemaps = [...text.matchAll(/^Sitemap:\s*(\S+)/gim)].map((match) => match[1]);
	if (sitemaps.length !== 1 || sitemaps[0] !== new URL('/sitemap.xml', siteUrl).href) errors.push('robots.txt must reference the canonical sitemap');
	return errors;
}

export function checkSeoHeaders(text) {
	const rules = new Map();
	let path;
	for (const line of text.split(/\r?\n/)) {
		if (line.startsWith('/')) {
			path = line.trim();
			rules.set(path, {});
		} else if (path && /^\s+[^:]+:/.test(line)) {
			const [, name, value] = /^\s+([^:]+):\s*(.*)$/.exec(line);
			rules.get(path)[name.toLowerCase()] = value;
		}
	}
	const noindexPaths = new Set([
		...locales.flatMap((locale) => {
			const prefix = locale === 'en' ? '' : `/${locale}`;
			return [`${prefix}/llms.txt`, `${prefix}/llms-full.txt`];
		}),
		'/llms.mdx/docs/*',
		'/api/search',
		'/install.sh',
		'/install.ps1',
		'/subscriptions/*',
	]);
	const errors = [...noindexPaths].filter((path) => rules.get(path)?.['x-robots-tag'] !== 'noindex').map((path) => `Missing noindex response header: ${path}`);
	for (const [path, headers] of rules) {
		if (headers['x-robots-tag']?.includes('noindex') && !noindexPaths.has(path)) errors.push(`Unexpected noindex response header: ${path}`);
	}
	for (const [path, type] of [
		['/robots.txt', 'text/plain; charset=utf-8'],
		['/sitemap.xml', 'application/xml; charset=utf-8'],
	]) {
		if (rules.get(path)?.['content-type'] !== type) errors.push(`Incorrect crawler file content type: ${path}`);
	}
	return errors;
}

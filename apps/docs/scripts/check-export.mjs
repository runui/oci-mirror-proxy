import { readFile, readdir, stat } from 'node:fs/promises';
import { resolve, relative, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { parse } from 'parse5';
import { repositoryAssets } from './prepare-assets.mjs';
import { checkPageSeo, checkRobots, checkSeoHeaders, checkSitemap } from './check-seo.mjs';
import { locales } from '../lib/locales.ts';

const origin = 'https://docs.example.test';
const siteUrl = new URL(process.env.SITE_URL || 'https://ocimp.zmide.com');

export function inspectHtml(html) {
	const ids = new Set();
	const references = [];
	const elements = [];
	function visit(node) {
		const attrs = Object.fromEntries((node.attrs ?? []).map(({ name, value }) => [name, value]));
		if (attrs.id) ids.add(attrs.id);
		if (node.tagName)
			elements.push({
				tag: node.tagName,
				attrs,
				text: (node.childNodes ?? [])
					.filter((child) => child.nodeName === '#text')
					.map((child) => child.value)
					.join(''),
			});
		for (const key of ['href', 'src', 'poster']) {
			if (attrs[key]) references.push(attrs[key]);
		}
		if (node.tagName === 'meta' && ['og:image', 'twitter:image'].includes(attrs.property ?? attrs.name) && attrs.content) references.push(attrs.content);
		for (const child of node.childNodes ?? []) visit(child);
	}
	visit(parse(html));
	return { ids, references, elements };
}

async function filesIn(directory) {
	return (
		await Promise.all(
			(await readdir(directory, { withFileTypes: true })).map((entry) => {
				const path = join(directory, entry.name);
				return entry.isDirectory() ? filesIn(path) : [path];
			}),
		)
	).flat();
}

export async function checkExport(directory, { siteChecks = true } = {}) {
	const root = resolve(directory);
	const files = await filesIn(root);
	const known = new Set(files);
	const documents = new Map();
	const errors = [];
	await Promise.all(
		files
			.filter((file) => file.endsWith('.html'))
			.map(async (file) => {
				documents.set(file, inspectHtml(await readFile(file, 'utf8')));
			}),
	);
	function findFile(pathname) {
		let path;
		try {
			path = resolve(root, `.${decodeURIComponent(pathname)}`);
		} catch {
			return;
		}
		if (path !== root && !path.startsWith(`${root}/`)) return;
		return [path, join(path, 'index.html'), `${path}.html`].find((candidate) => known.has(candidate));
	}
	function checkReference(reference, from) {
		if (/^(mailto:|tel:|data:|blob:)/.test(reference)) return;
		let url;
		try {
			url = new URL(reference, `${origin}${from}`);
		} catch {
			errors.push(`${from}: invalid URL ${reference}`);
			return;
		}
		if (url.origin !== origin && url.origin !== siteUrl.origin) return;
		const target = findFile(url.pathname);
		if (!target) {
			errors.push(`${from}: missing target ${reference}`);
			return;
		}
		const hash = url.hash.slice(1).split(':~:')[0];
		if (hash && documents.has(target)) {
			let id;
			try {
				id = decodeURIComponent(hash);
			} catch {
				errors.push(`${from}: invalid fragment ${reference}`);
				return;
			}
			if (!documents.get(target).ids.has(id)) errors.push(`${from}: missing anchor ${reference}`);
		}
	}
	for (const [file, document] of documents) {
		const from = `/${relative(root, file).replace(/index\.html$/, '')}`;
		for (const reference of document.references) checkReference(reference, from);
		if (siteChecks) {
			const lang = document.elements.find(({ tag }) => tag === 'html')?.attrs.lang;
			const expected = locales.find((locale) => from.startsWith(`/${locale}/`)) ?? 'en';
			if (lang !== expected) errors.push(`${from}: expected html lang=${expected}, got ${lang}`);
			if (!document.ids.has('main-content')) errors.push(`${from}: missing skip-link destination`);
		}
	}
	if (siteChecks) {
		await Promise.all(
			(await repositoryAssets()).map(async ({ path, source }) => {
				const target = join(root, path);
				if (!known.has(target)) errors.push(`Missing repository asset: /${path}`);
				else {
					const [output, original] = await Promise.all([readFile(target), readFile(source)]);
					const expected = path.startsWith('subscriptions/') && path.endsWith('.json') ? Buffer.from(JSON.stringify(JSON.parse(original.toString('utf8')))) : original;
					if (!output.equals(expected)) errors.push(`Repository asset differs from expected output: /${path}`);
				}
			}),
		);
		const required = ['/404.html', '/api/search', '/robots.txt', '/sitemap.xml', '/favicon.svg', '/_headers'];
		const contentFiles = await readdir(new URL('../content/docs/', import.meta.url));
		const slugs = contentFiles
			.filter((name) => name.endsWith('.mdx') && !locales.some((locale) => name.endsWith(`.${locale}.mdx`)))
			.map((name) => (name === 'index.mdx' ? '' : name.slice(0, -4)));
		const absolute = (path) => new URL(path, siteUrl).href;
		const prefix = (locale) => (locale === 'en' ? '' : `/${locale}`);
		const homeLanguages = { ...Object.fromEntries(locales.map((locale) => [locale, absolute(`${prefix(locale)}/`)])), 'x-default': absolute('/') };
		const seoPages = [
			...locales.map((locale) => ({ path: `${prefix(locale)}/`, languages: homeLanguages })),
			{ path: '/404.html' },
			{ path: '/404/' },
			{ path: '/_not-found/' },
		];
		for (const locale of locales) {
			required.push(`${prefix(locale)}/index.html`, `${prefix(locale)}/llms.txt`, `${prefix(locale)}/llms-full.txt`);
			for (const slug of slugs) {
				const parts = slug ? `${slug}/` : '';
				required.push(`${prefix(locale)}/docs/${parts}index.html`, `/llms.mdx/docs/${locale}/${parts}content.md`, `/og/docs/${locale}/${parts}image.png`);
				const translated = (language) => language === 'en' || contentFiles.includes(`${slug || 'index'}.${language}.mdx`);
				const en = absolute(`/docs/${parts}`);
				const fallback = !translated(locale);
				if (!fallback && locale !== 'en') {
					const original = documents.get(join(root, `docs/${parts}index.html`));
					const localized = documents.get(join(root, `${prefix(locale)}/docs/${parts}index.html`));
					for (const { tag, attrs } of original?.elements ?? []) {
						if (/^h[2-6]$/.test(tag) && attrs.id && localized && !localized.ids.has(attrs.id))
							errors.push(`${locale}/${slug}: missing shared heading anchor #${attrs.id}`);
					}
				}
				seoPages.push({
					path: `${prefix(locale)}/docs/${parts}`,
					...(fallback
						? { fallbackPath: `/docs/${parts}` }
						: {
								languages: {
									...Object.fromEntries(locales.filter(translated).map((language) => [language, absolute(`${prefix(language)}/docs/${parts}`)])),
									'x-default': en,
								},
							}),
				});
				if (fallback) {
					const document = documents.get(join(root, `${prefix(locale)}/docs/${parts}index.html`));
					if (document && !document.elements.some(({ attrs }) => attrs.class?.includes('translation-notice')))
						errors.push(`${locale}/${slug}: missing English fallback notice`);
				}
			}
		}
		for (const path of required) if (!known.has(join(root, path))) errors.push(`Missing required output: ${path}`);
		for (const page of seoPages) {
			const document = documents.get(findFile(page.path));
			if (document) errors.push(...checkPageSeo(document, { ...page, siteUrl }));
		}
		await Promise.all(
			files.map(async (file) => {
				if ((await stat(file)).size > 25 * 1024 * 1024) errors.push(`Cloudflare Pages file limit exceeded: ${relative(root, file)}`);
				if (/adaptive-downloads/.test(relative(root, file))) errors.push(`Excluded document was exported: ${relative(root, file)}`);
				if (/\.(txt|md)$/.test(file)) {
					const text = await readFile(file, 'utf8');
					for (const match of text.matchAll(/\]\(([^\s)]+)\)/g)) checkReference(match[1], `/${relative(root, file)}`);
					if (text.includes('adaptive-downloads')) errors.push(`Excluded document appears in ${relative(root, file)}`);
				}
				if (file.endsWith('.png')) {
					const bytes = await readFile(file);
					if (bytes.subarray(0, 8).toString('hex') !== '89504e470d0a1a0a') errors.push(`Invalid PNG: ${relative(root, file)}`);
				}
			}),
		);
		const searchFile = join(root, 'api/search');
		if (known.has(searchFile)) {
			const text = await readFile(searchFile, 'utf8');
			const data = JSON.parse(text);
			if (!data.i18n) errors.push('Search export must support all locales');
			if (text.includes('adaptive-downloads')) errors.push('Excluded document appears in search');
			for (const url of text.matchAll(/"url":"(\/[^"#]*)(#[^"]*)?"/g)) checkReference(url[1] + (url[2] ?? ''), '/api/search');
		}
		const sitemap = join(root, 'sitemap.xml');
		if (known.has(sitemap)) {
			const text = await readFile(sitemap, 'utf8');
			errors.push(
				...checkSitemap(
					text,
					seoPages.filter((page) => page.languages).map((page) => ({ url: absolute(page.path), languages: page.languages })),
				),
			);
			for (const match of text.matchAll(/<loc>(.*?)<\/loc>/g)) checkReference(new URL(match[1]).pathname, '/sitemap.xml');
		}
		const robots = join(root, 'robots.txt');
		if (known.has(robots)) errors.push(...checkRobots(await readFile(robots, 'utf8'), siteUrl));
		const headers = join(root, '_headers');
		if (known.has(headers)) errors.push(...checkSeoHeaders(await readFile(headers, 'utf8')));
	}
	return { errors: [...new Set(errors)], htmlCount: documents.size, fileCount: files.length };
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
	const result = await checkExport(process.argv[2] ?? fileURLToPath(new URL('../out/', import.meta.url)));
	if (result.errors.length) {
		console.error(result.errors.join('\n'));
		process.exitCode = 1;
	} else
		console.log(
			`Validated ${result.htmlCount} HTML pages and ${result.fileCount} exported files: repository assets, links, anchors, locales, search, Markdown, LLM indexes, social metadata, JSON-LD, robots, sitemap coverage, and fallback metadata.`,
		);
}

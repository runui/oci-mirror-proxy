import { readFile, readdir, stat } from 'node:fs/promises';
import { resolve, relative, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { parse } from 'parse5';
import { repositoryAssets } from './prepare-assets.mjs';
import { checkPageSeo, checkRobots, checkSeoHeaders, checkSitemap } from './check-seo.mjs';

const origin = 'https://docs.example.test';
const siteUrl = new URL(process.env.SITE_URL || 'https://ocimp.zmide.com');

export function inspectHtml(html) {
  const ids = new Set();
  const references = [];
  const elements = [];
  function visit(node) {
    const attrs = Object.fromEntries((node.attrs ?? []).map(({ name, value }) => [name, value]));
    if (attrs.id) ids.add(attrs.id);
    if (node.tagName) elements.push({ tag: node.tagName, attrs, text: (node.childNodes ?? []).filter((child) => child.nodeName === '#text').map((child) => child.value).join('') });
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
  return (await Promise.all((await readdir(directory, { withFileTypes: true })).map((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? filesIn(path) : [path];
  }))).flat();
}

export async function checkExport(directory, { siteChecks = true } = {}) {
  const root = resolve(directory);
  const files = await filesIn(root);
  const known = new Set(files);
  const documents = new Map();
  const errors = [];
  await Promise.all(files.filter((file) => file.endsWith('.html')).map(async (file) => {
    documents.set(file, inspectHtml(await readFile(file, 'utf8')));
  }));
  function findFile(pathname) {
    let path;
    try { path = resolve(root, `.${decodeURIComponent(pathname)}`); }
    catch { return; }
    if (path !== root && !path.startsWith(`${root}/`)) return;
    return [path, join(path, 'index.html'), `${path}.html`].find((candidate) => known.has(candidate));
  }
  function checkReference(reference, from) {
    if (/^(mailto:|tel:|data:|blob:)/.test(reference)) return;
    let url;
    try { url = new URL(reference, `${origin}${from}`); }
    catch { errors.push(`${from}: invalid URL ${reference}`); return; }
    if (url.origin !== origin && url.origin !== siteUrl.origin) return;
    const target = findFile(url.pathname);
    if (!target) { errors.push(`${from}: missing target ${reference}`); return; }
    const hash = url.hash.slice(1).split(':~:')[0];
    if (hash && documents.has(target)) {
      let id;
      try { id = decodeURIComponent(hash); }
      catch { errors.push(`${from}: invalid fragment ${reference}`); return; }
      if (!documents.get(target).ids.has(id)) errors.push(`${from}: missing anchor ${reference}`);
    }
  }
  for (const [file, document] of documents) {
    const from = `/${relative(root, file).replace(/index\.html$/, '')}`;
    for (const reference of document.references) checkReference(reference, from);
    if (siteChecks) {
      const lang = document.elements.find(({ tag }) => tag === 'html')?.attrs.lang;
      const expected = from.startsWith('/zh-CN/') ? 'zh-CN' : 'en';
      if (lang !== expected) errors.push(`${from}: expected html lang=${expected}, got ${lang}`);
      if (!document.ids.has('main-content')) errors.push(`${from}: missing skip-link destination`);
    }
  }
  if (siteChecks) {
    await Promise.all((await repositoryAssets()).map(async ({ path, source }) => {
      const target = join(root, path);
      if (!known.has(target)) errors.push(`Missing repository asset: /${path}`);
      else {
        const [output, original] = await Promise.all([readFile(target), readFile(source)]);
        const expected = path.startsWith('subscriptions/') && path.endsWith('.json')
          ? Buffer.from(JSON.stringify(JSON.parse(original.toString('utf8'))))
          : original;
        if (!output.equals(expected)) errors.push(`Repository asset differs from expected output: /${path}`);
      }
    }));
    const required = ['/index.html', '/zh-CN/index.html', '/404.html', '/api/search', '/robots.txt', '/sitemap.xml', '/favicon.svg', '/_headers', '/llms.txt', '/llms-full.txt', '/zh-CN/llms.txt', '/zh-CN/llms-full.txt'];
    const contentFiles = await readdir(new URL('../content/docs/', import.meta.url));
    const slugs = contentFiles.filter((name) => name.endsWith('.mdx') && !name.endsWith('.zh-CN.mdx')).map((name) => name === 'index.mdx' ? '' : name.slice(0, -4));
    const absolute = (path) => new URL(path, siteUrl).href;
    const homeLanguages = { en: absolute('/'), 'zh-CN': absolute('/zh-CN/'), 'x-default': absolute('/') };
    const seoPages = [
      { path: '/', languages: homeLanguages },
      { path: '/zh-CN/', languages: homeLanguages },
      { path: '/404.html' },
      { path: '/404/' },
      { path: '/_not-found/' },
    ];
    for (const locale of ['en', 'zh-CN']) {
      for (const slug of slugs) {
        const parts = slug ? `${slug}/` : '';
        required.push(`${locale === 'en' ? '' : '/zh-CN'}/docs/${parts}index.html`, `/llms.mdx/docs/${locale}/${parts}content.md`, `/og/docs/${locale}/${parts}image.png`);
        const translated = contentFiles.includes(`${slug || 'index'}.zh-CN.mdx`);
        const en = absolute(`/docs/${parts}`);
        seoPages.push({
          path: `${locale === 'en' ? '' : '/zh-CN'}/docs/${parts}`,
          ...(locale === 'zh-CN' && !translated ? { fallbackPath: `/docs/${parts}` } : {
            languages: { en, ...(translated ? { 'zh-CN': absolute(`/zh-CN/docs/${parts}`) } : {}), 'x-default': en },
          }),
        });
      }
    }
    for (const path of required) if (!known.has(join(root, path))) errors.push(`Missing required output: ${path}`);
    for (const page of seoPages) {
      const document = documents.get(findFile(page.path));
      if (document) errors.push(...checkPageSeo(document, { ...page, siteUrl }));
    }
    await Promise.all(files.map(async (file) => {
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
    }));
    const searchFile = join(root, 'api/search');
    if (known.has(searchFile)) {
      const text = await readFile(searchFile, 'utf8');
      const data = JSON.parse(text);
      if (!data.i18n) errors.push('Search export must support both locales');
      if (text.includes('adaptive-downloads')) errors.push('Excluded document appears in search');
      for (const url of text.matchAll(/"url":"(\/[^"#]*)(#[^"]*)?"/g)) checkReference(url[1] + (url[2] ?? ''), '/api/search');
    }
    const fallback = documents.get(join(root, 'zh-CN/docs/macos-installation/index.html'));
    if (fallback) {
      if (!fallback.elements.some(({ attrs }) => attrs.class?.includes('translation-notice'))) errors.push('Missing English fallback notice');
    }
    const sitemap = join(root, 'sitemap.xml');
    if (known.has(sitemap)) {
      const text = await readFile(sitemap, 'utf8');
      errors.push(...checkSitemap(text, seoPages.filter((page) => page.languages).map((page) => ({ url: absolute(page.path), languages: page.languages }))));
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
  } else console.log(`Validated ${result.htmlCount} HTML pages and ${result.fileCount} exported files: repository assets, links, anchors, locales, search, Markdown, LLM indexes, social metadata, JSON-LD, robots, sitemap coverage, and fallback metadata.`);
}

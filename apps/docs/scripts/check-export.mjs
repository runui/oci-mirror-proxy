import { readFile, readdir, stat } from 'node:fs/promises';
import { resolve, relative, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { parse } from 'parse5';

const origin = 'https://docs.example.test';

export function inspectHtml(html) {
  const ids = new Set();
  const references = [];
  const elements = [];
  function visit(node) {
    const attrs = Object.fromEntries((node.attrs ?? []).map(({ name, value }) => [name, value]));
    if (attrs.id) ids.add(attrs.id);
    if (node.tagName) elements.push({ tag: node.tagName, attrs });
    for (const key of ['href', 'src', 'poster']) {
      if (attrs[key]) references.push(attrs[key]);
    }
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
    if (url.origin !== origin) return;
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
    const required = ['/index.html', '/zh-CN/index.html', '/404.html', '/api/search', '/sitemap.xml', '/favicon.svg', '/_headers', '/llms.txt', '/llms-full.txt', '/zh-CN/llms.txt', '/zh-CN/llms-full.txt'];
    const slugs = (await readdir(new URL('../content/docs/', import.meta.url))).filter((name) => name.endsWith('.mdx') && !name.endsWith('.zh-CN.mdx')).map((name) => name === 'index.mdx' ? '' : name.slice(0, -4));
    for (const locale of ['en', 'zh-CN']) {
      for (const slug of slugs) {
        const parts = slug ? `${slug}/` : '';
        required.push(`${locale === 'en' ? '' : '/zh-CN'}/docs/${parts}index.html`, `/llms.mdx/docs/${locale}/${parts}content.md`, `/og/docs/${locale}/${parts}image.png`);
      }
    }
    for (const path of required) if (!known.has(join(root, path))) errors.push(`Missing required output: ${path}`);
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
      if (!fallback.elements.some(({ tag, attrs }) => tag === 'meta' && attrs.name === 'robots' && attrs.content.includes('noindex'))) errors.push('Fallback must be noindex');
      if (!fallback.elements.some(({ tag, attrs }) => tag === 'link' && attrs.rel === 'canonical' && new URL(attrs.href).pathname === '/docs/macos-installation/')) errors.push('Fallback canonical must point to English');
    }
    const sitemap = join(root, 'sitemap.xml');
    if (known.has(sitemap)) {
      const text = await readFile(sitemap, 'utf8');
      if (text.includes('/zh-CN/docs/macos-installation')) errors.push('Fallback must not be in sitemap');
      for (const match of text.matchAll(/<loc>(.*?)<\/loc>/g)) checkReference(new URL(match[1]).pathname, '/sitemap.xml');
    }
  }
  return { errors: [...new Set(errors)], htmlCount: documents.size, fileCount: files.length };
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const result = await checkExport(process.argv[2] ?? fileURLToPath(new URL('../out/', import.meta.url)));
  if (result.errors.length) {
    console.error(result.errors.join('\n'));
    process.exitCode = 1;
  } else console.log(`Validated ${result.htmlCount} HTML pages and ${result.fileCount} exported files: links, anchors, locales, search, Markdown, LLM indexes, OG images, sitemap, and fallback metadata.`);
}

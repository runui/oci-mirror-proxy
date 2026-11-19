import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('header destinations use native anchors rather than client routing', async () => {
  const source = await readFile(new URL('../components/site-header.tsx', import.meta.url), 'utf8');
  const links = source.slice(source.indexOf('export function SiteLinks'), source.indexOf('export function SiteHeader'));
  assert.match(links, /<a href=\{url\}/);
  assert.doesNotMatch(links, /<Link\b/);
});

test('site fonts cannot swap late and only include supported language subsets', async () => {
  const css = await readFile(new URL('../components/site-fonts.css', import.meta.url), 'utf8');
  const faces = css.match(/@font-face\s*\{[^}]+\}/g) ?? [];
  assert.equal(faces.length, 4);
  for (const face of faces) {
    assert.match(face, /font-display:\s*optional;/);
    assert.match(face, /unicode-range:/);
    assert.match(face, /geist(?:-mono)?-(?:latin|cyrillic)-wght-normal\.woff2/);
  }
  const layout = await readFile(new URL('../components/root-layout.tsx', import.meta.url), 'utf8');
  assert.match(layout, /import '\.\/site-fonts\.css'/);
  assert.doesNotMatch(layout, /import '@fontsource-variable\//);
});

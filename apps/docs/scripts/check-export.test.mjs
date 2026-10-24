import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { checkExport, inspectHtml } from './check-export.mjs';

test('parses entities, Unicode anchors, and asset references', () => {
  const document = inspectHtml('<h2 id="配置">Config</h2><a href="/next/?a=1&amp;b=2#配置">Next</a><img src="/demo.gif">');
  assert(document.ids.has('配置'));
  assert.deepEqual(document.references, ['/next/?a=1&b=2#配置', '/demo.gif']);
});

test('validates static directory routes, exact files, and anchors without a server', async (t) => {
  const root = await mkdtemp(join(tmpdir(), 'docs-export-test-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  await mkdir(join(root, 'next'));
  await writeFile(join(root, 'index.html'), '<a href="/next/#%E9%85%8D%E7%BD%AE">Next</a><a href="/api-search">Search</a><a href="https://example.com/missing">External</a>');
  await writeFile(join(root, 'next/index.html'), '<h2 id="配置">Config</h2><a href="../">Home</a>');
  await writeFile(join(root, 'api-search'), '{}');
  assert.deepEqual((await checkExport(root, { siteChecks: false })).errors, []);
  await writeFile(join(root, 'index.html'), '<a href="/next/#missing">Bad anchor</a><img src="/absent.gif">');
  const { errors } = await checkExport(root, { siteChecks: false });
  assert.equal(errors.length, 2);
  assert(errors.some((error) => error.includes('missing anchor')));
  assert(errors.some((error) => error.includes('missing target')));
});

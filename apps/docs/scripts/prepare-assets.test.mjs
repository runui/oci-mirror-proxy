import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { prepareAssets, repositoryAssets } from './prepare-assets.mjs';

test('compacts subscription JSON while preserving source files and other asset bytes', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'docs-assets-test-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const root = pathToFileURL(`${directory}/repository/`);
  const destination = pathToFileURL(`${directory}/public/`);
  const files = [
    'install.sh', 'install.ps1',
    'subscriptions/mirrors.json', 'subscriptions/schema.json',
    'subscriptions/README.md', 'subscriptions/README.zh-CN.md', 'subscriptions/nested/catalog.json',
    ...['direct-mirror.gif', 'oci-mirror-proxy.gif', 'direct-mirror.cast', 'oci-mirror-proxy.cast'].map((name) => `docs/demos/${name}`),
  ];
  const originals = new Map();
  for (const path of files) {
    const source = new URL(path, root);
    await mkdir(new URL('./', source), { recursive: true });
    const original = Buffer.from(path.endsWith('.json')
      ? `${JSON.stringify({ file: path, name: '中文 mirror with spaces', values: [true, false, null, 1.5], nested: { text: 'line one\nline two "quoted"' } }, null, 2)}\n`
      : `original bytes: ${path}\r\n\0`);
    originals.set(path, original);
    await writeFile(source, original);
  }
  assert.equal(await prepareAssets({ root, destination }), files.length);
  const assets = await repositoryAssets(root);
  for (const { path, source } of assets) {
    const original = await readFile(source);
    const sourcePath = path.startsWith('demos/') ? `docs/${path}` : path;
    assert.deepEqual(original, originals.get(sourcePath));
    const output = await readFile(new URL(path, destination));
    if (path.endsWith('.json')) {
      assert.deepEqual(JSON.parse(output), JSON.parse(original));
      assert.equal(output.toString(), JSON.stringify(JSON.parse(original)));
      assert(output.length < original.length);
    } else {
      assert.deepEqual(output, original);
    }
  }
  await writeFile(new URL('install.sh', root), '#!/bin/sh\n# updated\n');
  await rm(new URL('subscriptions/nested/catalog.json', root));
  await writeFile(new URL('favicon.svg', destination), '<svg/>');
  await prepareAssets({ root, destination });
  assert.equal(await readFile(new URL('install.sh', destination), 'utf8'), '#!/bin/sh\n# updated\n');
  await assert.rejects(readFile(new URL('subscriptions/nested/catalog.json', destination)), { code: 'ENOENT' });
  assert.equal(await readFile(new URL('favicon.svg', destination), 'utf8'), '<svg/>');
  await rm(new URL('install.ps1', root));
  await assert.rejects(prepareAssets({ root, destination }), { code: 'ENOENT' });
});

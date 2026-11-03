import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { coordinates } from './image.mjs';
import { API, publish, validate, verify } from './publish.mjs';

test('release tags reject invalid input', () => {
  for (const tag of ['v1.2.3', 'v1.2.3-rc.1', 'v1.2.3+build']) validate(tag);
  for (const tag of ['', '1.2.3', 'v../bad', 'v1\n']) assert.throws(() => validate(tag));
});

test('both providers replace, upload, verify and finalize assets', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'release-test-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const file = join(directory, 'asset.zip');
  await writeFile(file, 'archive');
  for (const github of [true, false]) {
    const calls = [];
    const api = {
      github,
      async request(method, path, options = {}) {
        calls.push({ method, path, ...options });
        if (method === 'GET') return null;
        if (method === 'POST' && path.endsWith('/releases')) return { id: 1, upload_url: 'https://uploads.github.com/assets{?name}' };
        return {};
      },
      async assets() { return [{ id: 2, name: 'asset.zip', size: 7 }]; },
    };
    await publish(api, 'owner/repo', 'v1.2.3-rc.1', [file]);
    assert.ok(calls.some((call) => call.method === 'DELETE'));
    assert.deepEqual(calls.at(-1).payload, { draft: false, prerelease: true });
    const uploads = calls.filter((call) => call.data);
    assert.equal(uploads.length, 1);
    assert.ok(uploads[0].data.includes(Buffer.from('archive')));
  }
});

test('checksum validation enforces all ten platforms and detects corruption', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'checksum-test-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const lines = [];
  const platforms = ['linux_amd64', 'linux_arm64', 'linux_armv7', 'linux_riscv64',
    'darwin_amd64', 'darwin_arm64', 'freebsd_amd64', 'freebsd_arm64', 'windows_amd64', 'windows_arm64'];
  for (const platform of platforms) {
    const name = `oci-mirror-proxy_v1_${platform}.${platform.startsWith('windows') ? 'zip' : 'tar.gz'}`;
    await writeFile(join(directory, name), 'archive');
    lines.push(`${createHash('sha256').update('archive').digest('hex')}  ${name}`);
  }
  await writeFile(join(directory, 'SHA256SUMS'), lines.join('\n'));
  assert.equal((await verify(directory, 'v1')).length, 11);
  await writeFile(join(directory, 'oci-mirror-proxy_v1_linux_amd64.tar.gz'), 'changed');
  await assert.rejects(verify(directory, 'v1'), /Checksum mismatch/);
  await writeFile(join(directory, 'SHA256SUMS'), lines[0]);
  await assert.rejects(verify(directory, 'v1'), /platform set/);
});

test('image tagging retains dev, stable and prerelease rules', () => {
  for (const [ref, version, stable] of [
    ['refs/heads/master', 'dev', 'false'], ['refs/tags/v1.2.3', 'v1.2.3', 'true'],
    ['refs/tags/v1.2.3-rc.1', 'v1.2.3-rc.1', 'false'],
  ]) {
    const result = coordinates('https://git.example.com/', 'Owner/Repo', ref);
    assert.deepEqual(result, { registry: 'git.example.com', repository: 'owner/repo', version, stable });
  }
  assert.throws(() => coordinates('https://git.example.com', 'owner/repo', 'refs/tags/v1+invalid'));
});

test('API rejects foreign upload hosts before sending credentials', async () => {
  const api = new API('https://api.github.com', 'test-token', true);
  await assert.rejects(api.request('POST', 'https://attacker.example/assets'), /Untrusted API URL/);
});

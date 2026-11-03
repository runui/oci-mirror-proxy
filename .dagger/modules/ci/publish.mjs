import { createHash, randomUUID } from 'node:crypto';
import { readFile, stat } from 'node:fs/promises';
import { basename, join } from 'node:path';
import { parseArgs } from 'node:util';
import { pathToFileURL } from 'node:url';

export function validate(tag) {
  if (!/^v[a-zA-Z0-9._+\-]+$/.test(tag) || tag.includes('\n')) throw new Error('Invalid release tag');
}

export async function verify(directory, tag) {
  validate(tag);
  const platforms = ['linux_amd64', 'linux_arm64', 'linux_armv7', 'linux_riscv64',
    'darwin_amd64', 'darwin_arm64', 'freebsd_amd64', 'freebsd_arm64', 'windows_amd64', 'windows_arm64'];
  const expected = new Set(platforms.map((platform) =>
    `oci-mirror-proxy_${tag}_${platform}.${platform.startsWith('windows') ? 'zip' : 'tar.gz'}`));
  const checksums = new Map();
  for (const line of (await readFile(join(directory, 'SHA256SUMS'), 'utf8')).trim().split('\n')) {
    const match = /^([0-9a-f]{64})\s+(.+)$/.exec(line);
    if (!match || basename(match[2]) !== match[2] || checksums.has(match[2])) {
      throw new Error('Invalid or duplicate checksum entry');
    }
    checksums.set(match[2], match[1]);
  }
  if (checksums.size !== expected.size || [...checksums.keys()].some((name) => !expected.has(name))) {
    throw new Error('Release platform set does not match the expected matrix');
  }
  for (const [name, digest] of checksums) {
    if (createHash('sha256').update(await readFile(join(directory, name))).digest('hex') !== digest) {
      throw new Error(`Checksum mismatch: ${name}`);
    }
  }
  return [...expected].sort().map((name) => join(directory, name)).concat(join(directory, 'SHA256SUMS'));
}

export class API {
  constructor(base, token, github) {
    this.base = base.replace(/\/$/, '');
    this.token = token;
    this.github = github;
  }

  async request(method, path, { payload, data, contentType, missing = false } = {}) {
    const url = new URL(path.startsWith('https://') ? path : this.base + path);
    const allowed = new Set([new URL(this.base).host]);
    if (this.github) allowed.add('uploads.github.com');
    if (url.protocol !== 'https:' || !allowed.has(url.host) || url.username || url.password) {
      throw new Error('Untrusted API URL');
    }
    if (payload !== undefined) {
      data = JSON.stringify(payload);
      contentType = 'application/json';
    }
    const response = await fetch(url, {
      method, body: data, redirect: 'error', signal: AbortSignal.timeout(120_000),
      headers: {
        Authorization: `${this.github ? 'Bearer' : 'token'} ${this.token}`,
        Accept: 'application/json', 'User-Agent': 'oci-mirror-proxy-release',
        ...(contentType ? { 'Content-Type': contentType } : {}),
      },
    });
    if (missing && response.status === 404) return null;
    if (!response.ok) throw new Error(`API ${method} failed with HTTP ${response.status}`);
    const body = await response.text();
    return body ? JSON.parse(body) : null;
  }

  async assets(path) {
    const items = [];
    for (let page = 1; ; page++) {
      const batch = await this.request('GET', `${path}?per_page=100&limit=100&page=${page}`);
      items.push(...batch);
      if (batch.length < 100) return items;
    }
  }
}

export async function publish(api, repository, tag, files) {
  let path = `/repos/${repository}/releases`;
  let release = await api.request('GET', `${path}/tags/${encodeURIComponent(tag)}`, { missing: true });
  if (!release) {
    release = await api.request('POST', path, { payload: {
      tag_name: tag, name: tag, draft: true, prerelease: tag.includes('-'),
      ...(api.github ? { generate_release_notes: true } : {
        body: 'Linux, macOS, FreeBSD, and Windows binaries. Verify downloads with SHA256SUMS.',
      }),
    } });
  }
  path += `/${release.id}`;
  const assetsPath = `${path}/assets`;
  for (const file of files) {
    const name = basename(file);
    for (const asset of await api.assets(assetsPath)) {
      if (asset.name === name) await api.request('DELETE', `${assetsPath}/${asset.id}`);
    }
    const data = await readFile(file);
    if (api.github) {
      await api.request('POST', `${release.upload_url.split('{')[0]}?name=${encodeURIComponent(name)}`, {
        data, contentType: 'application/octet-stream',
      });
    } else {
      const boundary = randomUUID();
      const body = Buffer.concat([
        Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="attachment"; filename="${name}"\r\nContent-Type: application/octet-stream\r\n\r\n`),
        data, Buffer.from(`\r\n--${boundary}--\r\n`),
      ]);
      await api.request('POST', `${assetsPath}?name=${encodeURIComponent(name)}`, {
        data: body, contentType: `multipart/form-data; boundary=${boundary}`,
      });
    }
  }
  const assets = await api.assets(assetsPath);
  for (const file of files) {
    const size = (await stat(file)).size;
    if (assets.filter((asset) => asset.name === basename(file) && asset.size === size).length !== 1) {
      throw new Error(`Uploaded asset verification failed: ${basename(file)}`);
    }
  }
  await api.request('PATCH', path, { payload: { draft: false, prerelease: tag.includes('-') } });
}

async function main() {
  const { values, positionals } = parseArgs({ allowPositionals: true, options: {
    tag: { type: 'string' }, provider: { type: 'string', default: 'github' },
    server: { type: 'string', default: 'https://github.com' }, repository: { type: 'string' },
    directory: { type: 'string', default: '/assets' },
  } });
  const [operation] = positionals;
  if (!['validate', 'verify', 'publish', 'notify'].includes(operation)) throw new Error('Invalid operation');
  validate(values.tag ?? '');
  if (operation === 'validate') return;
  if (operation === 'verify') return verify(values.directory, values.tag);
  if (operation === 'notify' && values.tag.includes('-')) return;
  if (!process.env.API_TOKEN) throw new Error('An API token is required');
  if (!['github', 'gitea'].includes(values.provider)) throw new Error('Invalid provider');
  const github = values.provider === 'github';
  const api = new API(github ? 'https://api.github.com' : values.server.replace(/\/$/, '') + '/api/v1', process.env.API_TOKEN, github);
  if (operation === 'notify') {
    await api.request('POST', '/repos/zmide/homebrew-tap/dispatches', { payload: {
      event_type: 'update-package', client_payload: { package: 'oci-mirror-proxy', tag: values.tag },
    } });
  } else {
    if (!/^[\w.\-]+\/[\w.\-]+$/.test(values.repository ?? '')) throw new Error('Invalid repository');
    await publish(api, values.repository, values.tag, await verify(values.directory, values.tag));
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) await main();

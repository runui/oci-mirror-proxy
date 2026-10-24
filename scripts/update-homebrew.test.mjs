import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { renderFormula } from './update-homebrew.mjs';

const platforms = ['darwin_arm64', 'darwin_amd64', 'linux_arm64', 'linux_amd64'];
const tag = 'v1.2.3';
const sums = platforms.map((platform, index) => (
  `${(index + 10).toString(16).repeat(64)}  oci-mirror-proxy_${tag}_${platform}.tar.gz`
));

test('renders all Homebrew platforms with release URLs and checksums', () => {
  const formula = renderFormula(tag, `${sums.join('\n')}\n`);
  assert.match(formula, /version "1\.2\.3"/);
  for (const [index, platform] of platforms.entries()) {
    assert.ok(formula.includes(`https://github.com/runui/oci-mirror-proxy/releases/download/${tag}/oci-mirror-proxy_${tag}_${platform}.tar.gz`));
    assert.ok(formula.includes(`sha256 "${(index + 10).toString(16).repeat(64)}"`));
  }
  assert.ok(formula.includes('run [opt_bin/"oci-mirror-proxy", "serve"]'));
  assert.ok(formula.includes('assert_equal "v#{version}"'));
  assert.ok(!formula.includes('system "sudo"'));
});

test('accepts CRLF, uppercase checksums, binary markers, and unrelated assets', () => {
  const entries = sums.map((line) => {
    const [sum, name] = line.split('  ');
    return `${sum.toUpperCase()} *${name}`;
  });
  entries.push(`${'A'.repeat(64)} *oci-mirror-proxy_${tag}_windows_amd64.zip`);
  assert.equal(renderFormula(tag, entries.join('\r\n')), renderFormula(tag, sums.join('\n')));
});

test('rejects prereleases, unsafe tags, and malformed versions', () => {
  for (const invalid of ['v1.2.3-beta.1', '1.2.3', 'v1.2', 'v1.2.3+build', 'v1.2.3\n', 'v1.2.3"; system "id']) {
    assert.throws(() => renderFormula(invalid, sums.join('\n')), /stable release tag/);
  }
});

test('rejects missing, malformed, duplicate, and mismatched checksums', () => {
  assert.throws(() => renderFormula(tag, sums.slice(1).join('\n')), /Missing checksum/);
  assert.throws(() => renderFormula(tag, ''), /Invalid SHA256SUMS/);
  assert.throws(() => renderFormula(tag, sums.join('\n').replace('a'.repeat(64), 'invalid')), /Invalid SHA256SUMS/);
  assert.throws(() => renderFormula(tag, [...sums, sums[0]].join('\n')), /Duplicate checksum/);
  assert.throws(() => renderFormula('v1.2.4', sums.join('\n')), /Missing checksum/);
});

test('checked-in formula is reproducible from its version and checksums', async () => {
  const formula = await readFile(new URL('../Formula/oci-mirror-proxy.rb', import.meta.url), 'utf8');
  const version = /version "([\d.]+)"/.exec(formula)[1];
  const entries = [...formula.matchAll(/url "[^"\n]+\/([^/"\n]+)"\n\s+sha256 "([a-f0-9]{64})"/g)]
    .map(([, name, sum]) => `${sum}  ${name}`);
  assert.equal(entries.length, 4);
  assert.equal(formula, renderFormula(`v${version}`, entries.join('\n')));
});

test('CLI writes the formula only after successful validation', async (t) => {
  const cwd = await mkdtemp(path.join(tmpdir(), 'homebrew-formula-'));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  const checksumFile = path.join(cwd, 'SHA256SUMS');
  const output = path.join(cwd, 'formula.rb');
  const script = fileURLToPath(new URL('./update-homebrew.mjs', import.meta.url));
  await writeFile(checksumFile, sums.join('\n'));
  function run(...args) {
    return spawnSync(process.execPath, [script, ...args], { encoding: 'utf8' });
  }
  assert.equal(run().status, 1);
  assert.equal(run(tag, checksumFile, output).status, 0);
  assert.equal(await readFile(output, 'utf8'), renderFormula(tag, sums.join('\n')));
  await writeFile(checksumFile, 'invalid');
  assert.notEqual(run(tag, checksumFile, output).status, 0);
  assert.equal(await readFile(output, 'utf8'), renderFormula(tag, sums.join('\n')));
});

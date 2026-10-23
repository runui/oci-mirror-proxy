import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const hook = fileURLToPath(new URL('../.husky/pre-push', import.meta.url));
const script = fileURLToPath(new URL('./check-push-tags.sh', import.meta.url));
const zero = '0'.repeat(40);

test('tag push checks only the target remote local tracking branches', (t) => {
  const cwd = mkdtempSync(path.join(tmpdir(), 'check-push-tags-'));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  function git(...args) {
    const result = spawnSync('git', args, { cwd, encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
    return result.stdout.trim();
  }
  function check(input, remote = 'origin') {
    return spawnSync('sh', [script, remote, '/unreachable/remote'], {
      cwd, input, encoding: 'utf8',
    });
  }
  function tagLine(oid, name = 'v1') {
    return `refs/tags/${name} ${oid} refs/tags/${name} ${zero}\n`;
  }
  git('init', '-q');
  git('config', 'user.name', 'Hook Test');
  git('config', 'user.email', 'hook@example.com');
  git('remote', 'add', 'origin', '/unreachable/origin');
  git('remote', 'add', 'other', '/unreachable/other');
  git('commit', '--allow-empty', '-qm', 'first');
  const first = git('rev-parse', 'HEAD');
  git('tag', 'lightweight');
  git('tag', '-a', 'annotated', '-m', 'release');
  const annotated = git('rev-parse', 'annotated');

  assert.equal(check('').status, 0);
  assert.equal(check(`refs/heads/main ${first} refs/heads/main ${zero}\n`).status, 0);
  assert.equal(check(`(delete) ${zero} refs/tags/v1 ${first}\n`).status, 0);
  assert.equal(check(tagLine(first)).status, 1);
  git('update-ref', 'refs/remotes/other/main', first);
  assert.equal(check(tagLine(first)).status, 1);
  assert.equal(check(tagLine(first), 'other').status, 0);
  assert.equal(check(tagLine(first), '/unreachable/origin').status, 1);

  git('update-ref', 'refs/remotes/origin/main', first);
  assert.equal(check(tagLine(first)).status, 0);
  assert.equal(check(tagLine(annotated)).status, 0);
  git('commit', '--allow-empty', '-qm', 'second');
  const second = git('rev-parse', 'HEAD');
  assert.equal(check(tagLine(second)).status, 1);
  assert.equal(check(`refs/heads/main ${second} refs/heads/main ${first}\n${tagLine(second)}`).status, 1);
  assert.equal(check(tagLine(first) + tagLine(second, 'v2')).status, 1);
  git('update-ref', 'refs/remotes/origin/main', second);
  assert.equal(check(tagLine(first)).status, 0);
  assert.equal(check(tagLine(second)).status, 0);
  const tree = git('rev-parse', 'HEAD^{tree}');
  assert.equal(check(tagLine(tree)).status, 1);

  // Exercise the Husky entry point from the repository root as well.
  const entry = spawnSync('sh', [hook, 'origin', '/unreachable/origin'], {
    cwd: fileURLToPath(new URL('../', import.meta.url)), input: '', encoding: 'utf8',
  });
  assert.equal(entry.status, 0, entry.stderr);
});

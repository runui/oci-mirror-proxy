import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmod, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

test('CI setup pins the Docker engine and disables Cloud credentials and prompts', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'setup-dagger-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const bin = join(directory, 'bin');
  await mkdir(bin);
  const curl = join(bin, 'curl');
  await writeFile(curl, `#!/bin/sh
set -eu
test "$1" = '-fsSL'
test "$2" = 'https://dl.dagger.io/dagger/install.sh'
test "$3" = '-o'
cat > "$4" <<'INSTALLER'
set -eu
mkdir -p "$BIN_DIR"
printf '#!/bin/sh\\ntest "$1" = version\\n' > "$BIN_DIR/dagger"
chmod +x "$BIN_DIR/dagger"
INSTALLER
`);
  await chmod(curl, 0o755);
  const githubEnv = join(directory, 'github-env');
  const githubPath = join(directory, 'github-path');
  const result = spawnSync('sh', ['scripts/setup-dagger.sh'], {
    encoding: 'utf8',
    env: {
      ...process.env,
      HOME: directory,
      PATH: `${bin}:${process.env.PATH}`,
      GITHUB_ENV: githubEnv,
      GITHUB_PATH: githubPath,
      DAGGER_ENGINE: 'cloud',
      DAGGER_CLOUD_TOKEN: 'fixture-cloud-token',
    },
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(await readFile(githubPath, 'utf8'), `${directory}/.local/bin\n`);
  assert.equal(await readFile(githubEnv, 'utf8'),
    'DAGGER_ENGINE=image+docker://registry.dagger.io/engine:v1.0.0-beta.24\n'
    + 'DAGGER_CLOUD_TOKEN=\nDAGGER_NO_NAG=1\n');
});

test('Dagger workflow secrets use provider URLs', async () => {
  let secrets = 0;
  for (const file of ['.gitea/workflows/container.yml', '.gitea/workflows/release.yml', '.github/workflows/release.yml']) {
    const workflow = await readFile(file, 'utf8');
    for (const match of workflow.matchAll(/--(?:password|token)=(\S+)/g)) {
      assert.match(match[1], /^env:\/\/[A-Z_]+$/, file);
      secrets++;
    }
  }
  assert.equal(secrets, 4);
});

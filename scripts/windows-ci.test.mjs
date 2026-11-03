import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

test('credentialed Windows checks launch a script file, not an oversized inline command', async () => {
  const script = await readFile('internal/tests/install/test-windows-user.ps1', 'utf8');
  const launch = script.split('\n').find((line) => line.includes('Start-Process') && line.includes('-Credential'));
  assert.ok(launch);
  assert.doesNotMatch(launch, /-EncodedCommand/);
  assert.match(launch, /'-File',\('\"\{0\}\"' -f \$checkScript\)/);
  assert.match(launch, /-LoadUserProfile/);
  assert.match(script, /WriteAllText\(\$checkScript, \$command, \[Text.Encoding\]::UTF8\)/);
  assert.match(script, /Check must run as a standard user/);
});

test('Windows artifacts upload from one workspace directory', async () => {
  const workflow = await readFile('.github/workflows/windows.yml', 'utf8');
  const upload = workflow.split('- name: Upload Windows service diagnostics')[1].split('- name:')[0];
  assert.match(upload, /path: windows-service-diagnostics\s/);
  assert.doesNotMatch(upload, /runner\.temp|C:\//);
  assert.match(workflow, /Join-Path \$env:GITHUB_WORKSPACE 'windows-service-diagnostics'/);
  assert.match(workflow, /Copy-Item -LiteralPath \$entry.Value/);
});

test('standard-user diagnostics survive work directory cleanup', async () => {
  const script = await readFile('internal/tests/install/test-windows-user.ps1', 'utf8');
  const capture = script.indexOf("Join-Path $env:WINDOWS_SERVICE_DIAGNOSTICS $name");
  const cleanup = script.indexOf('Remove-Item -LiteralPath $WorkDirectory -Recurse -Force');
  assert.ok(capture > 0 && cleanup > capture);
  assert.match(script.slice(capture, cleanup), /Copy-Item -LiteralPath \$_.FullName -Destination \$diagnostics/);
});

test('rollback checks wait for the restarted service before injecting failure', async () => {
  const script = await readFile('internal/tests/install/test-windows-service.ps1', 'utf8');
  assert.match(script, /Start-Service -Name \$serviceName\s+Wait-ProxyReady \$installed \$pipe/);
});

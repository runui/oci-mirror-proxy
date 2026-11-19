import test from 'node:test';
import assert from 'node:assert/strict';
import { runInNewContext } from 'node:vm';
import { detectPlatform, platformBootstrap, platformSelection } from '../lib/platform-bootstrap.ts';

test('detects desktop systems before rendering, with Linux fallback', () => {
	for (const [ua, expected] of [['Windows NT 10.0', 'windows'], ['Macintosh; Intel Mac OS X', 'macos'], ['FreeBSD amd64', 'freebsd'], ['Linux', 'linux'], ['iPhone; CPU iPhone OS like Mac OS X', 'linux']]) {
		assert.equal(detectPlatform(ua), expected);
		const document = { documentElement: { dataset: {} } };
		runInNewContext(platformBootstrap, { document, navigator: { userAgent: ua } });
		assert.equal(document.documentElement.dataset.installPlatform, expected);
	}
});

test('initializes native radio selection without waiting for React', () => {
	const input = {};
	runInNewContext(platformSelection, { document: {
		documentElement: { dataset: { installPlatform: 'macos' } },
		getElementById(id) { assert.equal(id, 'install-platform-macos'); return input; },
	} });
	assert.deepEqual(input, { checked: true, defaultChecked: true });
});

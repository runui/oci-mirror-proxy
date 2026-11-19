import test from 'node:test';
import assert from 'node:assert/strict';
import { runInNewContext } from 'node:vm';
import { localeBootstrap } from '../lib/locale-bootstrap.ts';

function run({ pathname = '/', languages = ['zh-CN'], saved = null, blocked = false, fail = false } = {}) {
	const result = { target: null, hidden: false };
	runInNewContext(localeBootstrap, {
		location: { pathname, search: '?from=test', hash: '#install', replace(target) { if (fail) throw new Error('blocked'); result.target = target; } },
		navigator: { languages, language: 'en-US' },
		localStorage: { getItem() { if (blocked) throw new Error('blocked'); return saved; } },
		document: {
			createElement() { return { remove() { result.hidden = false; } }; },
			head: { appendChild(style) { assert.equal(style.textContent, 'html{visibility:hidden}'); result.hidden = true; } },
		},
	});
	return result;
}

test('negotiates before rendering and preserves query and fragment', () => {
	assert.deepEqual(run(), { target: '/zh-CN/?from=test#install', hidden: true });
	assert.equal(run({ languages: ['de-DE', 'ru-RU'] }).target, '/ru/?from=test#install');
	assert.equal(run({ languages: ['ZH-tw'] }).target, '/zh-CN/?from=test#install');
});

test('respects explicit English preference and language ordering without hiding content', () => {
	for (const options of [{ saved: 'en' }, { languages: ['en-US', 'zh'] }, { languages: ['fr'] }]) {
		assert.deepEqual(run(options), { target: null, hidden: false });
	}
	assert.equal(run({ saved: 'ru' }).target, '/ru/?from=test#install');
});

test('does not redirect deep links and tolerates unavailable storage or navigation', () => {
	for (const pathname of ['/zh-CN/', '/ru/', '/docs/installation/']) {
		assert.deepEqual(run({ pathname }), { target: null, hidden: false });
	}
	assert.equal(run({ blocked: true }).target, '/zh-CN/?from=test#install');
	assert.deepEqual(run({ fail: true }), { target: null, hidden: false });
});

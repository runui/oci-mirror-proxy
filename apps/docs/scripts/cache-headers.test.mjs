import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const headers = await readFile(new URL('../public/_headers', import.meta.url), 'utf8');
const rules = headers.split(/\r?\n/).reduce((rules, line) => {
	if (line.startsWith('/')) rules.push({ path: line.trim(), headers: [] });
	else if (line.trim()) rules.at(-1)?.headers.push(line.trim());
	return rules;
}, []);

test('content-addressed Next.js assets are cached without revalidation', () => {
	const rule = rules.find(({ path }) => path === '/_next/static/*');
	assert.ok(rule);
	assert.ok(rule.headers.includes('Cache-Control: public, max-age=31536000, immutable'));
});

test('immutable caching is restricted to content-addressed assets', () => {
	for (const rule of rules) {
		if (rule.headers.some((header) => /immutable/i.test(header))) {
			assert.equal(rule.path, '/_next/static/*');
		}
	}
	for (const path of ['/install.sh', '/install.ps1', '/subscriptions/*']) {
		assert.ok(rules.find((rule) => rule.path === path)?.headers.includes('Cache-Control: public, max-age=0, must-revalidate'));
	}
});

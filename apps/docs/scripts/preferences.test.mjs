import test from 'node:test';
import assert from 'node:assert/strict';
import { isLocale, localizedPath, preferredLocale } from '../lib/preferences.ts';

test('matches supported browser languages in preference order, with English fallback', () => {
	assert.equal(preferredLocale(['zh-CN', 'en-US']), 'zh-CN');
	assert.equal(preferredLocale(['zh-Hans-SG']), 'zh-CN');
	assert.equal(preferredLocale(['ZH-tw']), 'zh-CN');
	assert.equal(preferredLocale(['en-GB', 'zh-CN']), 'en');
	assert.equal(preferredLocale(['fr-FR', 'zh']), 'zh-CN');
	assert.equal(preferredLocale(['de-DE', 'ja-JP']), 'en');
	assert.equal(preferredLocale([]), 'en');
	assert.equal(preferredLocale(['zho']), 'en');
	assert.equal(preferredLocale(['ru-RU', 'en-US']), 'ru');
	assert.equal(preferredLocale(['RU', 'zh-CN']), 'ru');
	assert.equal(preferredLocale(['de-DE', 'ru-BY']), 'ru');
	assert.equal(preferredLocale(['en-US', 'ru']), 'en');
	assert.equal(preferredLocale(['rus']), 'en');
});

test('manual language selection takes precedence over the browser', () => {
	assert.equal(preferredLocale(['zh-CN'], 'en'), 'en');
	assert.equal(preferredLocale(['en-US'], 'zh-CN'), 'zh-CN');
	assert.equal(preferredLocale(['zh-CN'], 'invalid'), 'zh-CN');
	assert.equal(isLocale(null), false);
	assert.equal(isLocale('zh'), false);
	assert.equal(preferredLocale(['zh-CN'], 'ru'), 'ru');
	assert.equal(isLocale('ru'), true);
	assert.equal(isLocale('ru-RU'), false);
});

test('language switching preserves document paths and trailing slashes', () => {
	assert.equal(localizedPath('/', 'zh-CN'), '/zh-CN/');
	assert.equal(localizedPath('/zh-CN/', 'en'), '/');
	assert.equal(localizedPath('/zh-CN', 'en'), '/');
	assert.equal(localizedPath('/docs/configuration/', 'zh-CN'), '/zh-CN/docs/configuration/');
	assert.equal(localizedPath('/zh-CN/docs/configuration/', 'en'), '/docs/configuration/');
	assert.equal(localizedPath('/zh-CN/docs/configuration/', 'zh-CN'), '/zh-CN/docs/configuration/');
	assert.equal(localizedPath('/', 'ru'), '/ru/');
	assert.equal(localizedPath('/ru', 'en'), '/');
	assert.equal(localizedPath('/ru/', 'zh-CN'), '/zh-CN/');
	assert.equal(localizedPath('/zh-CN/docs/configuration/', 'ru'), '/ru/docs/configuration/');
	assert.equal(localizedPath('/ru/docs/configuration/', 'en'), '/docs/configuration/');
	assert.equal(localizedPath('/ru/docs/configuration/', 'zh-CN'), '/zh-CN/docs/configuration/');
	assert.equal(localizedPath('/ru/docs/configuration/', 'ru'), '/ru/docs/configuration/');
	assert.equal(localizedPath('/russian/', 'en'), '/russian/');
});

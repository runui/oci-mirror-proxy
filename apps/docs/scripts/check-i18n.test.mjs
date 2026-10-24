import test from 'node:test';
import assert from 'node:assert/strict';
import { checkTranslations, translationErrors } from './check-i18n.mjs';
import { messages } from '../lib/messages.ts';

test('all registered UI dictionaries match English and document coverage is reported per locale', async () => {
	const { errors, coverage } = await checkTranslations();
	assert.deepEqual(errors, []);
	assert.deepEqual(
		coverage.map(({ locale }) => locale),
		['en', 'zh-CN', 'ru'],
	);
	for (const { translated, total, missing } of coverage) assert.equal(translated + missing.length, total);
});

test('preserves Fumadocs single-brace placeholders as well as i18next interpolation', () => {
	const reference = { prompt: 'Read {url}', retry: 'Results for {query}', greeting: 'Hello {{name}}' };
	assert.deepEqual(translationErrors(reference, { prompt: '阅读 {url}', retry: '{query} 的结果', greeting: '你好 {{name}}' }), []);
	assert.equal(translationErrors(reference, { prompt: '阅读链接', retry: 'Результаты для {url}', greeting: 'Привет {{user}}' }).length, 3);
	assert.equal(messages('ru', 'fumadocs')['Read {url}, I want to ask questions about it.(page actions)'], 'Прочитай {url}, я хочу задать вопросы об этой странице.');
});

test('rejects missing, unknown, empty, mistyped, and structurally incompatible translations', () => {
	const reference = { title: 'Hello {{name}}', items: [{ label: 'One' }] };
	assert.deepEqual(translationErrors(reference, { title: 'Привет, {{name}}', items: [{ label: 'Один' }] }), []);
	const errors = translationErrors(reference, { title: 'Привет', items: [{ label: '' }, { label: 'Два' }], unknown: true });
	for (const message of ['placeholders differ', 'unknown translation key', 'array length differs', 'nonempty string']) assert(errors.some((error) => error.includes(message)));
	assert(translationErrors(reference, { items: {} }).some((error) => error.includes('structure differs')));
	assert(translationErrors(reference, { title: 42, items: [] }).some((error) => error.includes('nonempty string')));
});

test('i18next reads fixed languages without leaking one render language into another', async () => {
	const results = await Promise.all(['ru', 'en', 'zh-CN', 'ru'].map(async (locale) => messages(locale, 'ui').skip));
	assert.deepEqual(results, ['Перейти к содержимому', 'Skip to content', '跳转到正文', 'Перейти к содержимому']);
	assert.equal(messages('ru', 'displayName'), 'Русский');
	assert.equal(messages('ru', 'fumadocs')['Copy Text(code block)(aria-label)'], 'Копировать код');
	assert.equal(messages('unsupported', 'ui').skip, 'Skip to content');
});

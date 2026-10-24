import { readFile, readdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { locales } from '../lib/locales.ts';

export function translationErrors(reference, translated, path = '') {
	if (typeof reference === 'string') {
		const errors = [];
		if (typeof translated !== 'string' || !translated.trim()) return [`${path}: expected a nonempty string`];
		const placeholders = (text) => [...text.matchAll(/{{?\s*([^{}]+?)\s*}}?/g)].map((match) => match[1]).sort();
		if (JSON.stringify(placeholders(reference)) !== JSON.stringify(placeholders(translated))) errors.push(`${path}: interpolation placeholders differ`);
		return errors;
	}
	if (!translated || typeof translated !== 'object' || Array.isArray(reference) !== Array.isArray(translated)) return [`${path}: translation structure differs`];
	const errors = Object.keys(translated)
		.filter((key) => !Object.hasOwn(reference, key))
		.map((key) => `${path}.${key}: unknown translation key`);
	if (Array.isArray(reference) && reference.length !== translated.length) errors.push(`${path}: array length differs`);
	for (const [key, value] of Object.entries(reference)) {
		errors.push(...translationErrors(value, translated[key], path ? `${path}.${key}` : key));
	}
	return errors;
}

export async function checkTranslations() {
	const root = new URL('../locales/', import.meta.url);
	const reference = JSON.parse(await readFile(new URL('en/translation.json', root), 'utf8'));
	const errors = [];
	const coverage = [];
	const directories = (await readdir(root, { withFileTypes: true })).filter((entry) => entry.isDirectory()).map((entry) => entry.name);
	for (const directory of directories) if (!locales.includes(directory)) errors.push(`Unregistered locale directory: ${directory}`);
	const files = await readdir(new URL('../content/docs/', import.meta.url));
	const originals = files.filter((file) => file.endsWith('.mdx') && !locales.some((locale) => file.endsWith(`.${locale}.mdx`)));
	for (const file of files.filter((file) => file.endsWith('.mdx') && !originals.includes(file))) {
		const locale = locales.find((locale) => file.endsWith(`.${locale}.mdx`));
		if (!originals.includes(file.replace(`.${locale}.mdx`, '.mdx'))) errors.push(`Translation has no English original: ${file}`);
	}
	for (const locale of locales) {
		try {
			const translated = JSON.parse(await readFile(new URL(`${locale}/translation.json`, root), 'utf8'));
			errors.push(...translationErrors(reference, translated).map((error) => `${locale}: ${error}`));
		} catch (error) {
			errors.push(`${locale}: ${error.message}`);
		}
		const missing = locale === 'en' ? [] : originals.filter((file) => !files.includes(file.replace(/\.mdx$/, `.${locale}.mdx`)));
		coverage.push({ locale, translated: originals.length - missing.length, total: originals.length, missing });
	}
	return { errors, coverage };
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
	const { errors, coverage } = await checkTranslations();
	for (const { locale, translated, total, missing } of coverage) {
		console.log(`${locale}: ${translated}/${total} documents translated${missing.length ? `; English fallback: ${missing.join(', ')}` : ''}`);
	}
	if (errors.length) {
		console.error(errors.join('\n'));
		process.exitCode = 1;
	} else console.log('Translation keys, structures, and interpolation placeholders are valid.');
}

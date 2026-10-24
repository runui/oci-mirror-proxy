import { defineI18n } from 'fumadocs-core/i18n';
import { i18nProvider, uiTranslations } from 'fumadocs-ui/i18n';
import { locales, type Locale } from './locales';
import { messages } from './messages';

export type { Locale } from './locales';

// All language variants have real, pre-rendered routes; no middleware is required.
export const i18n = defineI18n({
	defaultLanguage: 'en',
	languages: [...locales],
	hideLocale: 'default-locale',
	fallbackLanguage: 'en',
});

const translations = i18n
	.translations()
	.extend(uiTranslations())
	.add(Object.fromEntries(locales.map((locale) => [locale, { displayName: messages(locale, 'displayName'), ...messages(locale, 'fumadocs') }])));

export function localeProvider(locale: Locale) {
	return i18nProvider(translations, locale);
}

export function homeUrl(locale: Locale) {
	return locale === 'en' ? '/' : `/${locale}/`;
}

export function docsUrl(locale: Locale, slug = '') {
	return `${locale === 'en' ? '' : `/${locale}`}/docs${slug ? `/${slug}` : ''}`;
}

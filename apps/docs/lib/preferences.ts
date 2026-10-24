import { locales, type Locale } from './locales.ts';

export const localeStorageKey = 'oci-mirror-proxy:locale';

export function isLocale(value: unknown): value is Locale {
	return locales.some((locale) => locale === value);
}

export function preferredLocale(languages: readonly string[], saved: string | null = null): Locale {
	if (isLocale(saved)) return saved;
	for (const language of languages) {
		if (/^zh(?:-|$)/i.test(language)) return 'zh-CN';
		if (/^en(?:-|$)/i.test(language)) return 'en';
		if (/^ru(?:-|$)/i.test(language)) return 'ru';
	}
	return 'en';
}

export function localizedPath(pathname: string, locale: Locale) {
	const prefix = locales.find((language) => language !== 'en' && (pathname === `/${language}` || pathname.startsWith(`/${language}/`)));
	const path = (prefix ? pathname.slice(prefix.length + 1) : pathname) || '/';
	return locale === 'en' ? path : `/${locale}${path}`;
}

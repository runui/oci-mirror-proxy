import { homeUrl, i18n, type Locale } from './i18n';
import { siteUrl } from './shared';
import { isEnglishFallback, source, type DocPage } from './source';
import { localizedMessages } from './messages';

export const homeTitles = Object.fromEntries(Object.entries(localizedMessages('seo')).map(([locale, text]) => [locale, text.title])) as Record<Locale, string>;

// HTML routes use the same trailing slash as the static export.
export function canonicalUrl(path: string) {
	return new URL(`${path.replace(/\/+$/, '')}/`, siteUrl).href;
}

export function homeLanguages() {
	return {
		...Object.fromEntries(i18n.languages.map((locale) => [locale, canonicalUrl(homeUrl(locale as Locale))])),
		'x-default': canonicalUrl(homeUrl('en')),
	};
}

export function pageSeo(page: DocPage) {
	const fallback = isEnglishFallback(page);
	const canonicalPage = fallback ? source.getPage(page.slugs, 'en')! : page;
	const languages = fallback
		? undefined
		: {
				...Object.fromEntries(
					i18n.languages.flatMap((locale) => {
						const translated = source.getPage(page.slugs, locale);
						return translated && !isEnglishFallback(translated) ? [[locale, canonicalUrl(translated.url)]] : [];
					}),
				),
				'x-default': canonicalUrl(source.getPage(page.slugs, 'en')!.url),
			};

	return { canonicalPage, canonical: canonicalUrl(canonicalPage.url), languages, fallback };
}

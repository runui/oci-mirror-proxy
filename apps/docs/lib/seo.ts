import { homeUrl, i18n, type Locale } from './i18n';
import { siteUrl } from './shared';
import { isEnglishFallback, source, type DocPage } from './source';

export const homeTitles = {
  en: 'A better path for Docker pulls',
  'zh-CN': '为 Docker 拉取提供更可靠的路径',
};

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
  const languages = fallback ? undefined : {
    ...Object.fromEntries(i18n.languages.flatMap((locale) => {
      const translated = source.getPage(page.slugs, locale);
      return translated && !isEnglishFallback(translated) ? [[locale, canonicalUrl(translated.url)]] : [];
    })),
    'x-default': canonicalUrl(source.getPage(page.slugs, 'en')!.url),
  };

  return { canonicalPage, canonical: canonicalUrl(canonicalPage.url), languages, fallback };
}

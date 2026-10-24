import type { Locale } from './i18n';

export const localeStorageKey = 'oci-mirror-proxy:locale';

export function isLocale(value: unknown): value is Locale {
  return value === 'en' || value === 'zh-CN';
}

export function preferredLocale(languages: readonly string[], saved: string | null = null): Locale {
  if (isLocale(saved)) return saved;
  for (const language of languages) {
    if (/^zh(?:-|$)/i.test(language)) return 'zh-CN';
    if (/^en(?:-|$)/i.test(language)) return 'en';
  }
  return 'en';
}

export function localizedPath(pathname: string, locale: Locale) {
  const path = pathname.replace(/^\/zh-CN(?=\/|$)/, '') || '/';
  return locale === 'en' ? path : `/zh-CN${path}`;
}

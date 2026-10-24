import type { Metadata } from 'next';
import { appName, getPageImageUrl, siteDescription, siteUrl } from './shared';
import { homeUrl, type Locale } from './i18n';
import { isEnglishFallback, source, type DocPage } from './source';

export function rootMetadata(locale: Locale): Metadata {
  return {
    metadataBase: siteUrl,
    title: { default: appName, template: `%s · ${appName}` },
    description: siteDescription,
    icons: { icon: '/favicon.svg' },
    openGraph: { type: 'website', siteName: appName, locale: locale === 'en' ? 'en_US' : 'zh_CN' },
    twitter: { card: 'summary_large_image' },
  };
}

export function pageMetadata(page: DocPage): Metadata {
  const fallback = isEnglishFallback(page);
  const canonical = fallback ? source.getPage(page.slugs, 'en')!.url : page.url;
  return {
    title: page.data.title,
    description: page.data.description,
    alternates: {
      canonical,
      languages: fallback ? undefined : Object.fromEntries(source.getLanguages().flatMap(({ language }) => {
        const translated = source.getPage(page.slugs, language);
        return translated && !isEnglishFallback(translated) ? [[language, translated.url]] : [];
      })),
    },
    robots: fallback ? { index: false, follow: true } : undefined,
    openGraph: { title: page.data.title, description: page.data.description, url: canonical, images: getPageImageUrl(page).url },
    twitter: { card: 'summary_large_image', images: getPageImageUrl(page).url },
  };
}

export function homeMetadata(locale: Locale): Metadata {
  const page = source.getPage([], locale)!;
  return {
    title: { absolute: `${appName} — ${locale === 'en' ? 'A better path for Docker pulls' : '为 Docker 拉取提供更可靠的路径'}` },
    alternates: { canonical: homeUrl(locale), languages: { en: '/', 'zh-CN': '/zh-CN/' } },
    openGraph: { images: getPageImageUrl(page).url },
  };
}

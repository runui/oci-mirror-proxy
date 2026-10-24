import type { Metadata } from 'next';
import { appName, getPageImageUrl, siteDescriptions, siteUrl } from './shared';
import { homeUrl, type Locale } from './i18n';
import { source, type DocPage } from './source';
import { canonicalUrl, homeLanguages, homeTitles, pageSeo } from './seo';

const socialLocales = { en: 'en_US', 'zh-CN': 'zh_CN' };

function socialMetadata({ title, description, url, locale, languages, image }: {
  title: string;
  description: string;
  url: string;
  locale: Locale;
  languages?: Record<string, string>;
  image: string;
}): Pick<Metadata, 'openGraph' | 'twitter'> {
  const images = [{ url: new URL(image, siteUrl).href, width: 1200, height: 630, type: 'image/png', alt: title }];
  return {
    openGraph: {
      type: 'website',
      siteName: appName,
      title,
      description,
      url,
      locale: socialLocales[locale],
      alternateLocale: Object.keys(languages ?? {}).flatMap((language) =>
        language !== locale && language in socialLocales ? [socialLocales[language as Locale]] : []),
      images,
    },
    twitter: { card: 'summary_large_image', title, description, images },
  };
}

export function rootMetadata(locale: Locale): Metadata {
  return {
    metadataBase: siteUrl,
    title: { default: appName, template: `%s · ${appName}` },
    description: siteDescriptions[locale],
    applicationName: appName,
    icons: { icon: '/favicon.svg' },
    robots: { index: true, follow: true },
    openGraph: { type: 'website', siteName: appName, locale: socialLocales[locale] },
    twitter: { card: 'summary_large_image' },
  };
}

export function pageMetadata(page: DocPage): Metadata {
  const { canonicalPage, canonical, languages, fallback } = pageSeo(page);
  const description = page.data.description ?? siteDescriptions[canonicalPage.locale as Locale];
  return {
    title: page.data.title,
    description,
    alternates: { canonical, languages },
    robots: { index: !fallback, follow: true },
    ...socialMetadata({
      title: `${page.data.title} · ${appName}`, description, url: canonical,
      locale: canonicalPage.locale as Locale, languages, image: getPageImageUrl(canonicalPage).url,
    }),
  };
}

export function homeMetadata(locale: Locale): Metadata {
  const page = source.getPage([], locale)!;
  const title = `${appName} — ${homeTitles[locale]}`;
  const canonical = canonicalUrl(homeUrl(locale));
  const languages = homeLanguages();
  return {
    title: { absolute: title },
    description: siteDescriptions[locale],
    alternates: { canonical, languages },
    ...socialMetadata({ title, description: siteDescriptions[locale], url: canonical, locale, languages, image: getPageImageUrl(page).url }),
  };
}

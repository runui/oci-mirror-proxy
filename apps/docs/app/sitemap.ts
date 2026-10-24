import type { MetadataRoute } from 'next';
import { isEnglishFallback, source } from '@/lib/source';
import { homeUrl, i18n, type Locale } from '@/lib/i18n';
import { canonicalUrl, homeLanguages, pageSeo } from '@/lib/seo';

export const dynamic = 'force-static';
export default function sitemap(): MetadataRoute.Sitemap {
  return [
    ...i18n.languages.map((locale) => ({
      url: canonicalUrl(homeUrl(locale as Locale)),
      alternates: { languages: homeLanguages() },
    })),
    ...source.getLanguages().flatMap(({ pages }) => pages.filter((page) => !isEnglishFallback(page)).map((page) => {
      const { canonical, languages } = pageSeo(page);
      return { url: canonical, alternates: { languages } };
    })),
  ];
}

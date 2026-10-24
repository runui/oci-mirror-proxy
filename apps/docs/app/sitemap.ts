import type { MetadataRoute } from 'next';
import { isEnglishFallback, source } from '@/lib/source';
import { siteUrl } from '@/lib/shared';

export const dynamic = 'force-static';
export default function sitemap(): MetadataRoute.Sitemap {
  const paths = ['/', '/zh-CN/', ...source.getLanguages().flatMap(({ pages }) => pages.filter((page) => !isEnglishFallback(page)).map((page) => page.url))];
  return paths.map((path) => ({ url: new URL(path, siteUrl).href }));
}

import { notFound } from 'next/navigation';
import { source } from './source';
import { pageMetadata } from './metadata';
import type { Locale } from './i18n';

export function documentationParams(locale: Locale) {
  return source.getPages(locale).map((page) => ({ slug: page.slugs }));
}

export function documentationMetadata(slug: string[] | undefined, locale: Locale) {
  const page = source.getPage(slug, locale);
  if (!page) notFound();
  return pageMetadata(page);
}

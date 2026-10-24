import Link from 'next/link';
import { notFound } from 'next/navigation';
import { DocsBody, DocsDescription, DocsPage, DocsTitle, MarkdownCopyButton, ViewOptionsPopover } from 'fumadocs-ui/layouts/docs/page';
import { getMDXComponents } from './mdx';
import { isEnglishFallback, source } from '@/lib/source';
import { getPageMarkdownUrl, githubUrl, gitConfig } from '@/lib/shared';
import type { Locale } from '@/lib/i18n';
import { documentationStructuredData } from '@/lib/structured-data';
import { JsonLd } from './json-ld';
import { uiContent } from '@/lib/ui-content';

export function DocumentationPage({ slug, locale }: { slug?: string[]; locale: Locale }) {
	const page = source.getPage(slug, locale);
	if (!page) notFound();
	const MDX = page.data.body;
	const fallback = isEnglishFallback(page);
	const markdownUrl = getPageMarkdownUrl(page).url;
	const text = uiContent[locale];

	return (
		<DocsPage id="main-content" toc={page.data.toc} full={page.data.full}>
			{!fallback && <JsonLd data={documentationStructuredData(page)} />}
			<DocsTitle>{page.data.title}</DocsTitle>
			<DocsDescription className="mb-0">{page.data.description}</DocsDescription>
			{fallback && (
				<aside className="translation-notice" aria-label={text.translation}>
					{text.fallback}{' '}
					<Link href={source.getPage(slug, 'en')!.url} hrefLang="en">
						{text.readEnglish}
					</Link>
				</aside>
			)}
			<div className="page-actions">
				<MarkdownCopyButton markdownUrl={markdownUrl} />
				<ViewOptionsPopover markdownUrl={markdownUrl} githubUrl={`${githubUrl}/blob/${gitConfig.branch}/apps/docs/content/docs/${page.path}`} />
			</div>
			<DocsBody lang={fallback ? 'en' : locale}>
				<MDX components={getMDXComponents()} />
			</DocsBody>
		</DocsPage>
	);
}

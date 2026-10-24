import { docsLlms, source } from '@/lib/source';
import { getPageMarkdownUrl } from '@/lib/shared';
import { notFound } from 'next/navigation';
import { isLocale } from '@/lib/preferences';

export const revalidate = false;
export const dynamic = 'force-static';
export const dynamicParams = false;

export async function GET(_req: Request, { params }: RouteContext<'/llms.mdx/docs/[[...slug]]'>) {
	const { slug } = await params;
	if (!slug || !isLocale(slug[0]) || slug.at(-1) !== 'content.md') notFound();
	const page = source.getPage(slug.slice(1, -1), slug[0]);
	if (!page) notFound();

	return new Response(await docsLlms.page(page), {
		headers: {
			'Content-Type': 'text/markdown',
		},
	});
}

export function generateStaticParams() {
	return source
		.getLanguages()
		.flatMap(({ pages }) => pages)
		.map((page) => ({
			slug: getPageMarkdownUrl(page).segments,
		}));
}

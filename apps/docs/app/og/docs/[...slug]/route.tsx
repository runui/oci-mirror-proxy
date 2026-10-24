import { source } from '@/lib/source';
import { notFound } from 'next/navigation';
import { ImageResponse } from 'next/og';
import { appName, getPageImageUrl } from '@/lib/shared';
import { Brand } from '@/components/brand';
import { isLocale } from '@/lib/preferences';

export const revalidate = false;
export const dynamic = 'force-static';
export const dynamicParams = false;

export async function GET(_req: Request, { params }: RouteContext<'/og/docs/[...slug]'>) {
	const { slug } = await params;
	if (!isLocale(slug[0]) || slug.at(-1) !== 'image.png') notFound();
	const page = source.getPage(slug.slice(1, -1), slug[0]);
	if (!page) notFound();

	return new ImageResponse(
		<div
			style={{
				display: 'flex',
				flexDirection: 'column',
				width: '100%',
				height: '100%',
				padding: '56px 64px',
				background: '#0c0c0c',
				color: '#fff',
				borderBottom: '16px solid rgba(34,211,238,0.3)',
			}}>
			<div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
				<div style={{ fontSize: 64, lineHeight: 1.15, fontWeight: 700 }}>{page.data.title}</div>
				<div style={{ fontSize: 36, lineHeight: 1.35, color: '#bdbdbd' }}>{page.data.description}</div>
			</div>
			<div
				style={{
					display: 'flex',
					alignItems: 'center',
					gap: 20,
					marginTop: 'auto',
					paddingTop: 24,
					borderTop: '4px dashed rgba(34,211,238,0.3)',
					color: '#22d3ee',
					fontSize: 40,
				}}>
				<Brand />
				<span>{appName}</span>
			</div>
		</div>,
		{ width: 1200, height: 630 },
	);
}

export function generateStaticParams() {
	return source
		.getLanguages()
		.flatMap(({ pages }) => pages)
		.map((page) => ({
			slug: getPageImageUrl(page).segments,
		}));
}

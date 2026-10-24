import { source } from '@/lib/source';
import { notFound } from 'next/navigation';
import { generateOGImage } from 'fumadocs-ui/og';
import { appName, getPageImageUrl } from '@/lib/shared';
import { Brand } from '@/components/brand';

export const revalidate = false;
export const dynamic = 'force-static';
export const dynamicParams = false;

export async function GET(_req: Request, { params }: RouteContext<'/og/docs/[...slug]'>) {
  const { slug } = await params;
  if (!['en', 'zh-CN'].includes(slug[0]) || slug.at(-1) !== 'image.png') notFound();
  const page = source.getPage(slug.slice(1, -1), slug[0]);
  if (!page) notFound();

  return generateOGImage({
    title: page.data.title,
    description: page.data.description,
    site: appName,
    primaryColor: 'rgba(34,211,238,0.3)',
    primaryTextColor: '#22d3ee',
    icon: <Brand />,
  });
}

export function generateStaticParams() {
  return source.getLanguages().flatMap(({ pages }) => pages).map((page) => ({
    slug: getPageImageUrl(page).segments,
  }));
}

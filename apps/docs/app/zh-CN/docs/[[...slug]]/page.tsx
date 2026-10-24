import { DocumentationPage } from '@/components/docs-page';
import { documentationMetadata, documentationParams } from '@/lib/docs-routes';

export const dynamicParams = false;
export default async function Page({ params }: PageProps<'/zh-CN/docs/[[...slug]]'>) {
  return <DocumentationPage locale="zh-CN" slug={(await params).slug} />;
}
export function generateStaticParams() { return documentationParams('zh-CN'); }
export async function generateMetadata({ params }: PageProps<'/zh-CN/docs/[[...slug]]'>) {
  return documentationMetadata((await params).slug, 'zh-CN');
}

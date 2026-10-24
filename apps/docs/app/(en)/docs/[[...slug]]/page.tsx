import { DocumentationPage } from '@/components/docs-page';
import { documentationMetadata, documentationParams } from '@/lib/docs-routes';

export const dynamicParams = false;
export default async function Page({ params }: PageProps<'/docs/[[...slug]]'>) {
  return <DocumentationPage locale="en" slug={(await params).slug} />;
}
export function generateStaticParams() { return documentationParams('en'); }
export async function generateMetadata({ params }: PageProps<'/docs/[[...slug]]'>) {
  return documentationMetadata((await params).slug, 'en');
}

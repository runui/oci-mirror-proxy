import { DocumentationPage } from '@/components/docs-page';
import { documentationMetadata, documentationParams } from '@/lib/docs-routes';

export const dynamicParams = false;
export default async function Page({ params }: PageProps<'/ru/docs/[[...slug]]'>) {
	return <DocumentationPage locale="ru" slug={(await params).slug} />;
}
export function generateStaticParams() {
	return documentationParams('ru');
}
export async function generateMetadata({ params }: PageProps<'/ru/docs/[[...slug]]'>) {
	return documentationMetadata((await params).slug, 'ru');
}

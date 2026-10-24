import { DocumentationLayout } from '@/components/docs-layout';

export default function Layout({ children }: LayoutProps<'/ru/docs'>) {
	return <DocumentationLayout locale="ru">{children}</DocumentationLayout>;
}

import { RootLayout } from '@/components/root-layout';
import { rootMetadata } from '@/lib/metadata';

export const metadata = rootMetadata('ru');

export default function Layout({ children }: LayoutProps<'/ru'>) {
	return <RootLayout locale="ru">{children}</RootLayout>;
}

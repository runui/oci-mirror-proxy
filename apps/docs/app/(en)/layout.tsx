import { RootLayout } from '@/components/root-layout';
import { rootMetadata } from '@/lib/metadata';

export const metadata = rootMetadata('en');

export default function Layout({ children }: LayoutProps<'/'>) {
  return <RootLayout locale="en">{children}</RootLayout>;
}

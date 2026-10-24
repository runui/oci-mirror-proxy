import { RootLayout } from '@/components/root-layout';
import { rootMetadata } from '@/lib/metadata';

export const metadata = rootMetadata('zh-CN');

export default function Layout({ children }: LayoutProps<'/zh-CN'>) {
  return <RootLayout locale="zh-CN">{children}</RootLayout>;
}

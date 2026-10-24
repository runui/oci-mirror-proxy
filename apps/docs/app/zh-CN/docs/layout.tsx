import { DocumentationLayout } from '@/components/docs-layout';

export default function Layout({ children }: LayoutProps<'/zh-CN/docs'>) {
  return <DocumentationLayout locale="zh-CN">{children}</DocumentationLayout>;
}

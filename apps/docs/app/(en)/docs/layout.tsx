import { DocumentationLayout } from '@/components/docs-layout';

export default function Layout({ children }: LayoutProps<'/docs'>) {
  return <DocumentationLayout locale="en">{children}</DocumentationLayout>;
}

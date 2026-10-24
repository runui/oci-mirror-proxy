import type { ReactNode } from 'react';
import { DocsLayout } from 'fumadocs-ui/layouts/docs';
import { source } from '@/lib/source';
import { baseOptions } from '@/lib/layout.shared';
import type { Locale } from '@/lib/i18n';
import { DocsContainer, DocsHeader, DocsSidebar } from './docs-chrome';
import { SidebarProvider, SidebarTrigger, useSidebar } from 'fumadocs-ui/components/sidebar/base';

export function DocumentationLayout({ children, locale }: { children: ReactNode; locale: Locale }) {
  return <DocsLayout tree={source.getPageTree(locale)} {...baseOptions(locale)} i18n sidebar={{ collapsible: false }} slots={{
    header: DocsHeader,
    container: DocsContainer,
    sidebar: { provider: SidebarProvider, root: DocsSidebar, trigger: SidebarTrigger, useSidebar },
  }}>{children}</DocsLayout>;
}

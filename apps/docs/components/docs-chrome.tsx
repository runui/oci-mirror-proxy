'use client';

import { useEffect, useRef, type ComponentProps, type CSSProperties } from 'react';
import { Menu, X } from 'lucide-react';
import { useI18n } from 'fumadocs-ui/contexts/i18n';
import { Container } from 'fumadocs-ui/layouts/docs/slots/container';
import { createPageTreeRenderer } from 'fumadocs-ui/components/sidebar/page-tree';
import {
  SidebarDrawerContent, SidebarDrawerOverlay, SidebarFolder, SidebarFolderContent,
  SidebarFolderLink, SidebarFolderTrigger, SidebarItem, SidebarSeparator, SidebarTrigger,
  SidebarViewport, useSidebar,
} from 'fumadocs-ui/components/sidebar/base';
import { SiteHeader } from './site-header';
import { navigationLinks } from '@/lib/layout.shared';
import type { Locale } from '@/lib/i18n';
import Link from 'next/link';

const PageTree = createPageTreeRenderer({
  SidebarFolder, SidebarFolderContent, SidebarFolderLink, SidebarFolderTrigger,
  SidebarItem: DocsNavigationItem,
  SidebarSeparator: (props) => <SidebarSeparator {...props} className="docs-nav-group" />,
});

function DocsNavigationItem(props: ComponentProps<typeof SidebarItem>) {
  const { setOpen } = useSidebar();
  return <SidebarItem {...props} aria-current={props.active ? 'page' : undefined} className="docs-nav-item" onClick={() => setOpen(false)} />;
}

export function DocsContainer(props: ComponentProps<'div'>) {
  return <Container {...props} className="site-docs" style={{
    gridTemplate: '"header header header header header" var(--site-header-height) ". sidebar toc-popover toc ." ". sidebar tabs toc ." ". sidebar main toc ." 1fr / minmax(1rem, 1fr) var(--fd-sidebar-col) minmax(0, calc(var(--site-width) - var(--fd-sidebar-width) - var(--fd-toc-width))) var(--fd-toc-width) minmax(1rem, 1fr)',
    '--fd-docs-row-1': 'calc(var(--fd-banner-height, 0px) + var(--site-header-height))',
    '--fd-docs-row-2': 'var(--fd-docs-row-1)',
    '--fd-docs-row-3': 'calc(var(--fd-docs-row-2) + var(--fd-toc-popover-height))',
    ...props.style,
  } as CSSProperties} />;
}

export function DocsHeader() {
  return <SiteHeader menu={<SidebarTrigger className="site-icon-button site-menu-trigger"><Menu size={18} /></SidebarTrigger>} />;
}

function MobileNavigation() {
  const { locale = 'en' } = useI18n();
  const { open, setOpen, mode } = useSidebar();
  const ref = useRef<HTMLElement>(null);
  const chinese = locale === 'zh-CN';

  useEffect(() => {
    if (!open || mode !== 'drawer') return;
    const previous = document.activeElement;
    const panel = ref.current;
    if (!panel) return;
    const focusable = () => Array.from(panel.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), [tabindex="0"]')).filter((element) => element.getClientRects().length > 0);
    focusable()[0]?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); setOpen(false); }
      if (event.key !== 'Tab') return;
      const elements = focusable();
      const first = elements[0];
      const last = elements.at(-1);
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      if (previous instanceof HTMLElement && previous.isConnected) previous.focus();
    };
  }, [open, mode, setOpen]);

  return <>
    <SidebarDrawerOverlay className="docs-drawer-overlay" />
    <SidebarDrawerContent ref={ref} className="docs-drawer" role="dialog" aria-modal={open || undefined} aria-labelledby="docs-menu-title" inert={!open}>
      <div className="docs-drawer-heading"><span id="docs-menu-title">{chinese ? '导航' : 'navigate'}</span><SidebarTrigger className="site-icon-button"><X size={18} /></SidebarTrigger></div>
      <SidebarViewport>
        <nav className="docs-mobile-links" aria-label={chinese ? '主导航' : 'Primary'}>{navigationLinks(locale as Locale).map(({ text, url, external }) => <Link key={url} href={url} target={external ? '_blank' : undefined} rel={external ? 'noreferrer' : undefined} onClick={() => setOpen(false)}>{text}</Link>)}</nav>
        <nav className="docs-nav-tree" aria-label={chinese ? '文档导航' : 'Documentation'}><PageTree /></nav>
      </SidebarViewport>
    </SidebarDrawerContent>
  </>;
}

export function DocsSidebar() {
  const { locale } = useI18n();
  return <>
    <aside id="nd-sidebar" className="docs-sidebar"><SidebarViewport><nav className="docs-nav-tree" aria-label={locale === 'zh-CN' ? '文档导航' : 'Documentation'}><PageTree /></nav></SidebarViewport></aside>
    <MobileNavigation />
  </>;
}

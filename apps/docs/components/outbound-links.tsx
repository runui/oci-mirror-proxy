'use client';

import { useEffect } from 'react';
import { withOutboundSource } from '@/lib/outbound-links';

export function OutboundLinks() {
  useEffect(() => {
    const currentUrl = window.location.href;

    function updateLink(link: HTMLAnchorElement) {
      const href = link.getAttribute('href');
      if (href === null) return;
      const trackedHref = withOutboundSource(href, currentUrl);
      if (trackedHref !== href) link.setAttribute('href', trackedHref);
    }

    function updateLinks(root: ParentNode) {
      root.querySelectorAll<HTMLAnchorElement>('a[href]').forEach(updateLink);
    }

    updateLinks(document.body);

    // Include links rendered by client navigation and portalled third-party menus.
    const observer = new MutationObserver((mutations) => {
      for (const mutation of mutations) {
        if (mutation.type === 'attributes') {
          if (mutation.target instanceof HTMLAnchorElement) updateLink(mutation.target);
          continue;
        }
        for (const node of mutation.addedNodes) {
          if (!(node instanceof Element)) continue;
          if (node instanceof HTMLAnchorElement) updateLink(node);
          updateLinks(node);
        }
      }
    });
    observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['href'] });
    return () => observer.disconnect();
  }, []);

  return null;
}

'use client';

import { useEffect } from 'react';
import { localeStorageKey, localizedPath, preferredLocale } from '@/lib/preferences';

export function LocaleRedirect() {
  useEffect(() => {
    // Deep links already identify their language. Only negotiate at the entry URL.
    if (window.location.pathname !== '/') return;
    let saved: string | null = null;
    try { saved = localStorage.getItem(localeStorageKey); } catch { /* Storage may be disabled. */ }
    const locale = preferredLocale(navigator.languages?.length ? navigator.languages : [navigator.language], saved);
    if (locale !== 'en') {
      window.location.replace(`${localizedPath('/', locale)}${window.location.search}${window.location.hash}`);
    }
  }, []);

  return null;
}

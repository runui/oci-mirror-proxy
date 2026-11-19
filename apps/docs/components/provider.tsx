'use client';
import dynamic from 'next/dynamic';
import { RootProvider } from 'fumadocs-ui/provider/next';
import { type ReactNode } from 'react';
import { localeProvider, type Locale } from '@/lib/i18n';
import { isLocale, localeStorageKey, localizedPath } from '@/lib/preferences';
import { OutboundLinks } from './outbound-links';

const SearchDialog = dynamic(() => import('./search'));

export function Provider({ children, locale }: { children: ReactNode; locale: Locale }) {
  function changeLocale(value: string) {
    if (!isLocale(value) || value === locale) return;
    try { localStorage.setItem(localeStorageKey, value); } catch { /* Navigation still works without storage. */ }
    // Languages have separate root layouts, so client routing would fetch RSC before reloading anyway.
    window.location.assign(`${localizedPath(window.location.pathname, value)}${window.location.search}${window.location.hash}`);
  }

  return (
    <RootProvider i18n={{ ...localeProvider(locale), onLocaleChange: changeLocale }} search={{ SearchDialog }} theme={{ defaultTheme: 'system', enableSystem: true }}>
      <OutboundLinks />
      {children}
    </RootProvider>
  );
}

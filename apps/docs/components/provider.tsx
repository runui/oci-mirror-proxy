'use client';
import SearchDialog from '@/components/search';
import { RootProvider } from 'fumadocs-ui/provider/next';
import { type ReactNode } from 'react';
import { useRouter } from 'next/navigation';
import { localeProvider, type Locale } from '@/lib/i18n';
import { isLocale, localeStorageKey, localizedPath } from '@/lib/preferences';
import { LocaleRedirect } from './locale-redirect';
import { OutboundLinks } from './outbound-links';

export function Provider({ children, locale }: { children: ReactNode; locale: Locale }) {
  const router = useRouter();

  function changeLocale(value: string) {
    if (!isLocale(value)) return;
    try { localStorage.setItem(localeStorageKey, value); } catch { /* Navigation still works without storage. */ }
    router.push(`${localizedPath(window.location.pathname, value)}${window.location.search}${window.location.hash}`);
  }

  return (
    <RootProvider i18n={{ ...localeProvider(locale), onLocaleChange: changeLocale }} search={{ SearchDialog }} theme={{ defaultTheme: 'system', enableSystem: true }}>
      <LocaleRedirect />
      <OutboundLinks />
      {children}
    </RootProvider>
  );
}

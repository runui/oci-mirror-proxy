'use client';
import SearchDialog from '@/components/search';
import { RootProvider } from 'fumadocs-ui/provider/next';
import { type ReactNode } from 'react';
import { localeProvider, type Locale } from '@/lib/i18n';

export function Provider({ children, locale }: { children: ReactNode; locale: Locale }) {
  return (
    <RootProvider i18n={localeProvider(locale)} search={{ SearchDialog }} theme={{ defaultTheme: 'system' }}>
      {children}
    </RootProvider>
  );
}

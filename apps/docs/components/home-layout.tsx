'use client';

import { HomeLayout } from 'fumadocs-ui/layouts/home';
import type { ComponentPropsWithoutRef, ReactNode } from 'react';
import { HomeHeader } from './site-header';
import { baseOptions } from '@/lib/layout.shared';
import type { Locale } from '@/lib/i18n';

function HomeContainer({ children, ...props }: ComponentPropsWithoutRef<'main'>) {
  return <div {...props} id="nd-home-layout">{children}</div>;
}

export function SiteHomeLayout({ children, locale }: { children: ReactNode; locale: Locale }) {
  return <HomeLayout {...baseOptions(locale)} i18n slots={{ header: HomeHeader, container: HomeContainer }}>{children}</HomeLayout>;
}

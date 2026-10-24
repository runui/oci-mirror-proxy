import type { BaseLayoutProps } from 'fumadocs-ui/layouts/shared';
import { appName, githubUrl } from './shared';
import { docsUrl, homeUrl, type Locale } from './i18n';
import { Brand } from '@/components/brand';

export function navigationLinks(locale: Locale) {
  return [
    { text: locale === 'en' ? 'install' : '安装', url: `${homeUrl(locale)}#install`, external: false },
    { text: locale === 'en' ? 'docs' : '文档', url: docsUrl(locale), external: false },
    { text: locale === 'en' ? 'releases' : '版本', url: `${githubUrl}/releases`, external: true },
  ];
}

export function baseOptions(locale: Locale = 'en'): BaseLayoutProps {
  return {
    nav: {
      title: <><Brand /><span>{appName}</span></>,
      url: homeUrl(locale),
    },
    githubUrl,
    links: navigationLinks(locale),
  };
}

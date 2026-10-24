import type { BaseLayoutProps } from 'fumadocs-ui/layouts/shared';
import { appName, githubUrl } from './shared';
import { docsUrl, homeUrl, type Locale } from './i18n';
import { Brand } from '@/components/brand';
import { uiContent } from './ui-content';

export function navigationLinks(locale: Locale) {
	return [
		{ text: uiContent[locale].install, url: `${homeUrl(locale)}#install`, external: false },
		{ text: uiContent[locale].docs, url: docsUrl(locale), external: false },
		{ text: uiContent[locale].releases, url: `${githubUrl}/releases`, external: true },
	];
}

export function baseOptions(locale: Locale = 'en'): BaseLayoutProps {
	return {
		nav: {
			title: (
				<>
					<Brand />
					<span>{appName}</span>
				</>
			),
			url: homeUrl(locale),
		},
		githubUrl,
		links: navigationLinks(locale),
	};
}

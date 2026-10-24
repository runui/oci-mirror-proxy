import { localizedMessages } from './messages';
import type { Locale } from './locales';

export const appName = 'oci-mirror-proxy';
export const siteDescriptions = Object.fromEntries(Object.entries(localizedMessages('seo')).map(([locale, text]) => [locale, text.description])) as Record<Locale, string>;
export const productionUrl = 'https://ocimp.zmide.com';
export const siteUrl = new URL(process.env.SITE_URL || productionUrl);
export const docsRoute = '/docs';
export const docsImageRoute = '/og/docs';
export const docsContentRoute = '/llms.mdx/docs';

export const gitConfig = {
	user: 'runui',
	repo: 'oci-mirror-proxy',
	branch: 'master',
};

export const githubUrl = `https://github.com/${gitConfig.user}/${gitConfig.repo}`;
export const installScriptUrl = `${productionUrl}/install`;
export const installCommand = `curl -fsSL ${installScriptUrl}.sh | sh`;

export function getPageMarkdownUrl(page: { slugs: string[]; locale?: string }) {
	const segments = [page.locale ?? 'en', ...page.slugs, 'content.md'];

	return { segments, url: `${docsContentRoute}/${segments.join('/')}` };
}

export function getPageImageUrl(page: { slugs: string[]; locale?: string }) {
	const segments = [page.locale ?? 'en', ...page.slugs, 'image.png'];

	return { segments, url: `${docsImageRoute}/${segments.join('/')}` };
}

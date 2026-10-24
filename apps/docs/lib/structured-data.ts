import { homeUrl, i18n, type Locale } from './i18n';
import { appName, githubUrl, siteDescriptions } from './shared';
import { source, type DocPage } from './source';
import { canonicalUrl, homeTitles } from './seo';

const websiteId = `${canonicalUrl('/')}#website`;
const softwareId = `${canonicalUrl('/')}#software`;

export function homeStructuredData(locale: Locale) {
	const url = canonicalUrl(homeUrl(locale));
	return {
		'@context': 'https://schema.org',
		'@graph': [
			{
				'@type': 'WebSite',
				'@id': websiteId,
				name: appName,
				url: canonicalUrl('/'),
				inLanguage: i18n.languages,
			},
			{
				'@type': 'SoftwareApplication',
				'@id': softwareId,
				name: appName,
				url: canonicalUrl('/'),
				description: siteDescriptions[locale],
				applicationCategory: 'DeveloperApplication',
				operatingSystem: 'Linux, macOS, Windows, FreeBSD',
				sameAs: githubUrl,
			},
			{
				'@type': 'WebPage',
				'@id': `${url}#webpage`,
				url,
				name: `${appName} — ${homeTitles[locale]}`,
				description: siteDescriptions[locale],
				inLanguage: locale,
				isPartOf: { '@id': websiteId },
				about: { '@id': softwareId },
			},
		],
	};
}

export function documentationStructuredData(page: DocPage) {
	const locale = page.locale as Locale;
	const url = canonicalUrl(page.url);
	const overview = source.getPage([], locale)!;
	const crumbs = [
		{ name: appName, item: canonicalUrl(homeUrl(locale)) },
		{ name: overview.data.title, item: canonicalUrl(overview.url) },
		...(page.slugs.length ? [{ name: page.data.title, item: url }] : []),
	];
	return {
		'@context': 'https://schema.org',
		'@graph': [
			{
				'@type': 'TechArticle',
				'@id': `${url}#article`,
				url,
				headline: page.data.title,
				description: page.data.description ?? siteDescriptions[locale],
				inLanguage: locale,
				mainEntityOfPage: url,
				isPartOf: { '@id': websiteId },
				about: { '@id': softwareId },
			},
			{
				'@type': 'BreadcrumbList',
				'@id': `${url}#breadcrumb`,
				itemListElement: crumbs.map((crumb, index) => ({ '@type': 'ListItem', position: index + 1, ...crumb })),
			},
		],
	};
}

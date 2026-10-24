'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { GitBranch, Languages, Menu, Moon, Sun, X } from 'lucide-react';
import { useI18n } from 'fumadocs-ui/contexts/i18n';
import { LanguageSelect } from 'fumadocs-ui/layouts/shared/slots/language-select';
import { SearchTrigger } from 'fumadocs-ui/layouts/shared/slots/search-trigger';
import { ThemeSwitch } from 'fumadocs-ui/layouts/shared/slots/theme-switch';
import { Popover, PopoverClose, PopoverContent, PopoverTrigger } from 'fumadocs-ui/components/ui/popover';
import { Brand } from './brand';
import { navigationLinks } from '@/lib/layout.shared';
import { homeUrl, type Locale } from '@/lib/i18n';
import { appName, githubUrl } from '@/lib/shared';
import type { ReactNode } from 'react';
import { uiContent } from '@/lib/ui-content';

export function SiteLinks({ mobile = false }: { mobile?: boolean }) {
	const { locale = 'en' } = useI18n();
	const pathname = usePathname();

	return navigationLinks(locale as Locale).map(({ text, url, external }) => {
		const active = !external && !url.includes('#') && pathname.startsWith(url);
		const link = (
			<Link href={url} aria-current={active ? 'page' : undefined} target={external ? '_blank' : undefined} rel={external ? 'noreferrer' : undefined}>
				{text}
			</Link>
		);
		return mobile ? <PopoverClose key={url} render={link} /> : <span key={url}>{link}</span>;
	});
}

export function SiteHeader({ menu }: { menu: ReactNode }) {
	const { locale = 'en' } = useI18n();
	const text = uiContent[locale as Locale];

	return (
		<header id="nd-nav" className="site-header">
			<div className="site-header-inner">
				<Link href={homeUrl(locale as Locale)} className="site-brand" aria-label={`${appName} — ${text.home}`}>
					<Brand />
					<span>{appName}</span>
				</Link>
				<nav className="site-navigation" aria-label={text.primary}>
					<SiteLinks />
				</nav>
				<div className="site-tools">
					<Popover>
						<PopoverTrigger className="site-icon-button site-theme" aria-label={text.changeTheme}>
							<Sun size={17} />
							<Moon size={17} />
						</PopoverTrigger>
						<PopoverContent className="site-theme-menu" align="end" aria-label={text.theme}>
							<ThemeSwitch mode="light-dark-system" className="site-theme-options" />
						</PopoverContent>
					</Popover>
					<SearchTrigger className="site-icon-button" />
					<LanguageSelect className="site-icon-button site-language">
						<Languages size={16} />
						<span>{text.language}</span>
					</LanguageSelect>
					<a className="site-icon-button site-github" href={githubUrl} target="_blank" rel="noopener noreferrer" aria-label="GitHub" title="GitHub">
						<GitBranch size={17} />
					</a>
					{menu}
				</div>
			</div>
		</header>
	);
}

export function HomeHeader() {
	const { locale = 'en' } = useI18n();
	const text = uiContent[locale as Locale];

	return (
		<SiteHeader
			menu={
				<Popover>
					<PopoverTrigger className="site-icon-button site-menu-trigger" aria-label={text.openMenu}>
						<Menu size={18} />
					</PopoverTrigger>
					<PopoverContent className="site-mobile-menu" align="end" aria-label={text.navigation}>
						<div className="site-menu-heading">
							<span>{text.navigate}</span>
							<PopoverClose className="site-icon-button" aria-label={text.closeMenu}>
								<X size={18} />
							</PopoverClose>
						</div>
						<nav aria-label={text.primary}>
							<SiteLinks mobile />
						</nav>
					</PopoverContent>
				</Popover>
			}
		/>
	);
}

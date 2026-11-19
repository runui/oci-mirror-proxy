import type { ReactNode } from 'react';
import { Provider } from './provider';
import type { Locale } from '@/lib/i18n';
import { uiContent } from '@/lib/ui-content';
import { localeBootstrap } from '@/lib/locale-bootstrap';
import { platformBootstrap } from '@/lib/platform-bootstrap';
import './site-fonts.css';
import '@/app/global.css';

export function RootLayout({ children, locale }: { children: ReactNode; locale: Locale }) {
	return (
		<html lang={locale} suppressHydrationWarning>
			<head>
				{locale === 'en' && <script dangerouslySetInnerHTML={{ __html: localeBootstrap }} />}
				<script dangerouslySetInnerHTML={{ __html: platformBootstrap }} />
			</head>
			<body>
				<a className="skip-link" href="#main-content">
					{uiContent[locale].skip}
				</a>
				<Provider locale={locale}>{children}</Provider>
			</body>
		</html>
	);
}

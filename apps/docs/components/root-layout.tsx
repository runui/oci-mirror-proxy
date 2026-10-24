import type { ReactNode } from 'react';
import { Provider } from './provider';
import type { Locale } from '@/lib/i18n';
import { uiContent } from '@/lib/ui-content';
import '@fontsource-variable/geist';
import '@fontsource-variable/geist-mono';
import '@/app/global.css';

export function RootLayout({ children, locale }: { children: ReactNode; locale: Locale }) {
	return (
		<html lang={locale} suppressHydrationWarning>
			<body>
				<a className="skip-link" href="#main-content">
					{uiContent[locale].skip}
				</a>
				<Provider locale={locale}>{children}</Provider>
			</body>
		</html>
	);
}

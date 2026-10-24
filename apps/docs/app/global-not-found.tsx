import type { Metadata } from 'next';
import Link from 'next/link';
import { RootLayout } from '@/components/root-layout';

export const metadata: Metadata = {
	title: 'Page not found · oci-mirror-proxy',
	description: 'This page could not be found. Visit the oci-mirror-proxy documentation to continue.',
	robots: { index: false, follow: true },
};

export default function NotFound() {
	return (
		<RootLayout locale="en">
			<main id="main-content" className="not-found">
				<p className="eyebrow">404 / NOT FOUND</p>
				<h1>Nothing at this address.</h1>
				<p>The page may have moved. Start with the documentation.</p>
				<nav aria-label="Documentation">
					<Link href="/docs/">English documentation →</Link>
					<Link href="/zh-CN/docs/" lang="zh-CN">
						简体中文文档 →
					</Link>
					<Link href="/ru/docs/" lang="ru">
						Документация на русском →
					</Link>
				</nav>
			</main>
		</RootLayout>
	);
}

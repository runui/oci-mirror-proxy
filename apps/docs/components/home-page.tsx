import Link from 'next/link';
import Image from 'next/image';
import { ImageZoom, Lightbox } from 'fumadocs-ui/components/image-zoom';
import { ArrowRight, ArrowUpRight, GitBranch } from 'lucide-react';
import { docsUrl, type Locale } from '@/lib/i18n';
import { githubUrl } from '@/lib/shared';
import { InstallPanel } from './install-panel';
import { SiteHomeLayout } from './home-layout';
import { homeContent } from '@/lib/home-content';
import { homeStructuredData } from '@/lib/structured-data';
import { JsonLd } from './json-ld';
import { uiContent } from '@/lib/ui-content';

export function HomePage({ locale }: { locale: Locale }) {
	const text = homeContent[locale];

	return (
		<SiteHomeLayout locale={locale}>
			<main id="main-content" className="home-frame">
				<JsonLd data={homeStructuredData(locale)} />
				<section className="hero framed">
					<h1>
						{text.title}
						<br />
						<span>{text.titleAccent}</span>
					</h1>
					<p className="hero-description">{text.description}</p>
					<InstallPanel locale={locale} />
				</section>

				<section className="next-step framed">
					<div>
						<p className="eyebrow">{text.nextLabel}</p>
						<h2>{text.nextTitle}</h2>
						<p>{text.nextText}</p>
					</div>
					<div className="next-command">
						<code>
							<span aria-hidden="true">$ </span>docker pull hello-world
						</code>
						<p>
							{text.nextNote}{' '}
							<Link href={docsUrl(locale, 'installation')}>
								{text.nextLink} <ArrowUpRight size={12} />
							</Link>
						</p>
					</div>
				</section>

				<div className="flow-line" aria-label={uiContent[locale].flow}>
					<span>dockerd</span>
					<span aria-hidden="true">──→</span>
					<strong>oci-mirror-proxy</strong>
					<span aria-hidden="true">──→</span>
					<span>{text.mirrors}</span>
				</div>

				<section className="feature-grid" aria-label={text.featuresLabel}>
					{text.features.map(({ icon: Icon, title, text, slug }, index) => (
						<Link key={slug} className="feature-card framed" href={docsUrl(locale, slug)}>
							<div className="feature-index">
								<Icon size={18} />
								<span>0{index + 1}</span>
							</div>
							<h2>{title}</h2>
							<p>{text}</p>
							<ArrowUpRight size={16} className="feature-arrow" />
						</Link>
					))}
				</section>

				<section className="preview-section framed">
					<div className="preview-summary">
						<div>
							<p className="eyebrow">{text.previewLabel}</p>
							<h2>{text.previewTitle}</h2>
							<p>{text.previewText}</p>
							<Link href={text.previewUrl}>
								{text.previewLink} <ArrowRight size={14} />
							</Link>
						</div>
						<div className="preview-metric">
							<strong>
								1.46<span>×</span>
							</strong>
							<span>{text.previewMetric}</span>
						</div>
					</div>
					<Lightbox>
						<div className="preview-recordings">
							{text.recordings.map(({ title, alt, file }) => (
								<figure key={file}>
									<figcaption>{title}</figcaption>
									<ImageZoom src={`/demos/${file}.gif`} alt={alt} caption={title}>
										<Image src={`/demos/${file}.gif`} alt={alt} width={1104} height={726} unoptimized />
									</ImageZoom>
								</figure>
							))}
						</div>
					</Lightbox>
					<p className="preview-note">{text.previewNote}</p>
				</section>
				<footer className="site-footer">
					<span>
						<GitBranch size={14} /> oci-mirror-proxy
					</span>
					<nav aria-label={text.footer}>
						<a href={githubUrl} target="_blank" rel="noopener noreferrer">
							GitHub <ArrowUpRight size={12} />
						</a>
						<a href={text.llmsUrl}>llms.txt</a>
						<a href="https://fumadocs.dev" target="_blank" rel="noopener noreferrer">
							{uiContent[locale].builtWith}
						</a>
					</nav>
				</footer>
			</main>
		</SiteHomeLayout>
	);
}

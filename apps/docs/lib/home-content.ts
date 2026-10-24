import { Radio, RotateCcw, ShieldCheck } from 'lucide-react';
import { docsUrl, homeUrl } from './i18n';
import { locales } from './locales';
import { messages } from './messages';

const features = [
	{ icon: Radio, slug: 'operations' },
	{ icon: ShieldCheck, slug: 'architecture' },
	{ icon: RotateCcw, slug: 'configuration' },
];
const recordings = ['direct-mirror', 'oci-mirror-proxy'];

function content(locale: (typeof locales)[number]) {
	const text = messages(locale, 'home');
	return {
		...text,
		features: text.features.map((feature, index) => ({ ...feature, ...features[index] })),
		recordings: text.recordings.map((recording, index) => ({ ...recording, file: recordings[index] })),
		previewUrl: `${docsUrl(locale)}#preview`,
		llmsUrl: `${homeUrl(locale)}llms.txt`,
	};
}

export const homeContent = Object.fromEntries(locales.map((locale) => [locale, content(locale)])) as Record<(typeof locales)[number], ReturnType<typeof content>>;

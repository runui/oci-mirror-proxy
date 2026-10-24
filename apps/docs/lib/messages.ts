import { createInstance } from 'i18next';
import en from '../locales/en/translation.json' with { type: 'json' };
import zh from '../locales/zh-CN/translation.json' with { type: 'json' };
import ru from '../locales/ru/translation.json' with { type: 'json' };
import { locales, type Locale } from './locales.ts';

const instance = createInstance();
void instance.init({
	resources: { en: { translation: en }, 'zh-CN': { translation: zh }, ru: { translation: ru } },
	lng: 'en',
	fallbackLng: 'en',
	supportedLngs: [...locales],
	initAsync: false,
	interpolation: { escapeValue: false },
});

// Fixed-language translators are safe to use across concurrent static renders.
export function messages<K extends keyof typeof en>(locale: Locale, section: K): (typeof en)[K] {
	return instance.getFixedT(locale)(section, { returnObjects: true }) as (typeof en)[K];
}

export function localizedMessages<K extends keyof typeof en>(section: K) {
	return Object.fromEntries(locales.map((locale) => [locale, messages(locale, section)])) as Record<Locale, (typeof en)[K]>;
}

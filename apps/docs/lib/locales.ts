export const locales = ['en', 'zh-CN', 'ru'] as const;

export type Locale = (typeof locales)[number];

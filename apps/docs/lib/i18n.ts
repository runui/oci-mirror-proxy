import { defineI18n } from 'fumadocs-core/i18n';
import { i18nProvider, uiTranslations } from 'fumadocs-ui/i18n';

export type Locale = 'en' | 'zh-CN';

// Both URL variants have real, pre-rendered routes; no middleware is required.
export const i18n = defineI18n({
  defaultLanguage: 'en',
  languages: ['en', 'zh-CN'],
  hideLocale: 'default-locale',
  fallbackLanguage: 'en',
});

const translations = i18n.translations().extend(uiTranslations()).add({
  en: { displayName: 'English' },
  'zh-CN': {
    displayName: '简体中文',
    'Search(search trigger)': '搜索文档',
    'Open Search(search trigger)(aria-label)': '搜索文档',
    'Search(search dialog)': '搜索文档',
    'No results found(search dialog)': '没有找到结果',
    'Search failed(search dialog)': '搜索失败',
    'Close Search(search dialog)(aria-label)': '关闭搜索',
    'Choose a language(language switcher)': '选择语言',
    'Choose a language(language switcher)(aria-label)': '选择语言',
    'On this page(table of contents)': '本页目录',
    'Table of Contents(inline table of contents)': '本页目录',
    'No Headings(table of contents)': '暂无标题',
    'Previous Page(pagination)': '上一页',
    'Next Page(pagination)': '下一页',
    'Copy Markdown(page actions)': '复制 Markdown',
    'Copied Markdown(page actions)': '已复制 Markdown',
    'Open(page actions)': '打开',
    'View as Markdown(page actions)': '查看 Markdown',
    'Open in GitHub(page actions)': '在 GitHub 中打开',
    'Edit on GitHub(edit page)': '在 GitHub 上编辑',
    'Copy Text(code block)(aria-label)': '复制代码',
    'Copied Text(code block)(aria-label)': '代码已复制',
    'Copy Anchor Link(heading anchor)(aria-label)': '复制标题链接',
    'Copied Anchor Link(heading anchor)(aria-label)': '标题链接已复制',
    'Open Sidebar(aria-label)': '打开导航',
    'Close Sidebar(aria-label)': '关闭导航',
    'Collapse Sidebar(sidebar)(aria-label)': '收起导航',
    'Open Sidebar(sidebar)(aria-label)': '打开导航',
    'Close Sidebar(sidebar)(aria-label)': '关闭导航',
    'Toggle Menu(home layout header)(aria-label)': '切换菜单',
    'Toggle Theme(theme switcher)(aria-label)': '切换主题',
    'Light(theme switcher)(aria-label)': '浅色',
    'Dark(theme switcher)(aria-label)': '深色',
    'System(theme switcher)(aria-label)': '跟随系统',
  },
});

export function localeProvider(locale: Locale) {
  return i18nProvider(translations, locale);
}

export function homeUrl(locale: Locale) {
  return locale === 'en' ? '/' : '/zh-CN/';
}

export function docsUrl(locale: Locale, slug = '') {
  return `${locale === 'en' ? '' : '/zh-CN'}/docs${slug ? `/${slug}` : ''}`;
}

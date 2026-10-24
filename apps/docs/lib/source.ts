import { llms, loader } from 'fumadocs-core/source';
import { docsRoute } from './shared';
import { i18n } from './i18n';
import { defineDocs } from 'fumadocs-mdx/macro';
import { metaSchema, pageSchema } from 'fumadocs-core/source/schema';

const docs = defineDocs({
  dir: 'content/docs',
  docs: {
    schema: pageSchema,
    postprocess: {
      includeProcessedMarkdown: true,
    },
  },
  meta: {
    schema: metaSchema,
  },
});

// See https://fumadocs.dev/docs/headless/source-api for more info
export const source = loader({
  baseUrl: docsRoute,
  i18n,
  source: docs.toFumadocsSource(),
  plugins: [],
});

export type DocPage = typeof source.$inferPage;

export function isEnglishFallback(page: DocPage) {
  return page.locale === 'zh-CN' && !page.path.endsWith('.zh-CN.mdx');
}

export const docsLlms = llms(source, {
  renderDescription: (node, { lang }) => {
    if (node.type === 'page') {
      const page = source.getNodePage(node, lang);
      if (page) return `${isEnglishFallback(page) ? '[English fallback] ' : ''}${page.data.description ?? ''}`;
    }
    return typeof node.description === 'string' ? node.description : '';
  },
  renderPage: async (page) => `# ${page.data.title} (${page.url})

${isEnglishFallback(page) ? '> 本页尚无中文翻译，以下显示英文原文。 / English fallback: translation is not yet available.\n\n' : ''}${await page.data.getText('processed')}`,
});

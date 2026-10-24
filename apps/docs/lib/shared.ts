export const appName = 'oci-mirror-proxy';
export const siteDescriptions = {
  en: 'A local, read-only registry mirror proxy for Docker. Manage upstreams, isolate failures, and resume interrupted layers.',
  'zh-CN': '面向 Docker 的本地只读镜像代理。统一管理镜像源，自动隔离故障，并在镜像层下载中断后切换源续传。支持 Linux、macOS、Windows 和 FreeBSD。',
};
export const productionUrl = 'https://ocimp.zmide.com';
export const siteUrl = new URL(process.env.SITE_URL || productionUrl);
export const docsRoute = '/docs';
export const docsImageRoute = '/og/docs';
export const docsContentRoute = '/llms.mdx/docs';

export const gitConfig = {
  user: 'runui',
  repo: 'oci-mirror-proxy',
  branch: 'master',
};

export const githubUrl = `https://github.com/${gitConfig.user}/${gitConfig.repo}`;
export const installScriptUrl = `${productionUrl}/install`;
export const installCommand = `curl -fsSL ${installScriptUrl}.sh | sh`;

export function getPageMarkdownUrl(page: { slugs: string[]; locale?: string }) {
  const segments = [page.locale ?? 'en', ...page.slugs, 'content.md'];

  return { segments, url: `${docsContentRoute}/${segments.join('/')}` };
}

export function getPageImageUrl(page: { slugs: string[]; locale?: string }) {
  const segments = [page.locale ?? 'en', ...page.slugs, 'image.png'];

  return { segments, url: `${docsImageRoute}/${segments.join('/')}` };
}

export const appName = 'oci-mirror-proxy';
export const siteDescription = 'A local, read-only registry mirror proxy for Docker. Manage upstreams, isolate failures, and resume interrupted layers.';
export const siteUrl = new URL(process.env.SITE_URL || process.env.CF_PAGES_URL || 'http://localhost:3000');
export const docsRoute = '/docs';
export const docsImageRoute = '/og/docs';
export const docsContentRoute = '/llms.mdx/docs';

export const gitConfig = {
  user: 'runui',
  repo: 'oci-mirror-proxy',
  branch: 'master',
};

export const githubUrl = `https://github.com/${gitConfig.user}/${gitConfig.repo}`;
export const installCommand = 'curl -fsSL https://github.com/runui/oci-mirror-proxy/raw/refs/heads/master/install.sh | DOWNLOAD_BASE_URL=https://github.com/runui/oci-mirror-proxy sh -s -- --prefix "$HOME/.local"';

export function getPageMarkdownUrl(page: { slugs: string[]; locale?: string }) {
  const segments = [page.locale ?? 'en', ...page.slugs, 'content.md'];

  return { segments, url: `${docsContentRoute}/${segments.join('/')}` };
}

export function getPageImageUrl(page: { slugs: string[]; locale?: string }) {
  const segments = [page.locale ?? 'en', ...page.slugs, 'image.png'];

  return { segments, url: `${docsImageRoute}/${segments.join('/')}` };
}

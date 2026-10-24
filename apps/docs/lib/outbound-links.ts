export function withOutboundSource(href: string, currentUrl: string): string {
  let current: URL;
  let target: URL;
  try {
    current = new URL(currentUrl);
    target = new URL(href, current);
  } catch {
    return href;
  }

  if (!current.hostname || !['http:', 'https:'].includes(target.protocol) || target.origin === current.origin) return href;
  if (target.searchParams.getAll('utm_source').length === 1 && target.searchParams.get('utm_source') === current.hostname) return href;

  target.searchParams.set('utm_source', current.hostname);
  return target.href;
}

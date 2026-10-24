import test from 'node:test';
import assert from 'node:assert/strict';
import { withOutboundSource } from '../lib/outbound-links.ts';

const currentUrl = 'https://docs.example.test/docs/installation/?lang=en#download';

test('adds the current domain to external HTTP and HTTPS links', () => {
  assert.equal(withOutboundSource('https://github.com/runui/oci-mirror-proxy', currentUrl), 'https://github.com/runui/oci-mirror-proxy?utm_source=docs.example.test');
  assert.equal(withOutboundSource('http://example.net/guide', currentUrl), 'http://example.net/guide?utm_source=docs.example.test');
  assert.equal(withOutboundSource('//example.net/guide', currentUrl), 'https://example.net/guide?utm_source=docs.example.test');
  assert.equal(withOutboundSource('https://preview.example.test/', currentUrl), 'https://preview.example.test/?utm_source=docs.example.test');
});

test('uses the runtime domain without the scheme, port, path, query, or fragment', () => {
  assert.equal(withOutboundSource('https://example.net/', 'http://localhost:3001/zh-CN/?lang=zh#install'), 'https://example.net/?utm_source=localhost');
  assert.equal(withOutboundSource('https://example.net/', 'https://preview.example.test/docs/'), 'https://example.net/?utm_source=preview.example.test');
});

test('preserves existing query values and fragments', () => {
  const href = 'https://example.net/search?q=container%20images&tag=a&tag=b&utm_medium=docs#results';
  const tracked = new URL(withOutboundSource(href, currentUrl));
  assert.equal(tracked.searchParams.get('q'), 'container images');
  assert.deepEqual(tracked.searchParams.getAll('tag'), ['a', 'b']);
  assert.equal(tracked.searchParams.get('utm_medium'), 'docs');
  assert.equal(tracked.searchParams.get('utm_source'), 'docs.example.test');
  assert.equal(tracked.hash, '#results');
});

test('replaces stale or duplicate source values and is idempotent', () => {
  const href = 'https://example.net/?utm_source=old&utm_source=other#section';
  const tracked = withOutboundSource(href, currentUrl);
  assert.deepEqual(new URL(tracked).searchParams.getAll('utm_source'), ['docs.example.test']);
  assert.equal(withOutboundSource(tracked, currentUrl), tracked);

  const alreadyTracked = 'https://example.net/?q=container%20images&utm_source=docs.example.test#section';
  assert.equal(withOutboundSource(alreadyTracked, currentUrl), alreadyTracked);
});

test('leaves same-origin links unchanged', () => {
  for (const href of ['', '#download', '?lang=zh', '../configuration/', '/docs/', '/install.sh', 'https://docs.example.test/docs/?q=hello%20world#section', '//docs.example.test/docs/']) {
    assert.equal(withOutboundSource(href, currentUrl), href);
  }
});

test('leaves non-web schemes and malformed URLs unchanged', () => {
  for (const href of ['mailto:help@example.net', 'tel:+123456789', 'data:text/plain,hello', 'blob:https://example.net/file', 'ftp://example.net/file', 'javascript:void(0)', 'https://[invalid']) {
    assert.equal(withOutboundSource(href, currentUrl), href);
  }
  assert.equal(withOutboundSource('https://example.net/', 'invalid'), 'https://example.net/');
});

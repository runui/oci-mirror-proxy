import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { staticClient } from 'fumadocs-core/search/client/orama-static';

const data = await readFile(new URL('../out/api/search', import.meta.url));
const from = `data:application/json;base64,${data.toString('base64')}`;
await Promise.all([['en', 'circuit'], ['zh-CN', '熔断']].map(async ([locale, query]) => {
  const results = await staticClient({ from, locale }).search(query);
  assert(results.length > 0, `${locale}: no results for ${query}`);
  assert(results.every(({ url }) => locale === 'en' ? url.startsWith('/docs') : url.startsWith('/zh-CN/docs')), `${locale}: results from the wrong locale`);
  assert(results.every(({ url }) => !url.includes('adaptive-downloads')), 'Excluded document in search results');
  console.log(`Static search: ${locale}, ${JSON.stringify(query)} → ${results.length} localized results.`);
}));

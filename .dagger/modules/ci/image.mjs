import { pathToFileURL } from 'node:url';

export function coordinates(server, repository, ref) {
  const url = new URL(server);
  if (url.protocol !== 'https:' || !url.host || url.username || url.password) throw new Error('Expected an HTTPS registry server');
  repository = repository.toLowerCase();
  if (!/^[a-z0-9_.-]+\/[a-z0-9_.-]+$/.test(repository)) throw new Error('Invalid image repository');
  const version = ref.startsWith('refs/tags/v') ? ref.slice('refs/tags/'.length) : 'dev';
  if (!/^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$/.test(version) || version.includes('\n')) throw new Error('Invalid container tag');
  return { registry: url.host, repository, version, stable: String(version.startsWith('v') && !version.includes('-')) };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [field, server, repository, ref] = process.argv.slice(2);
  const result = coordinates(server, repository, ref);
  if (!Object.hasOwn(result, field)) throw new Error('Invalid coordinate field');
  process.stdout.write(result[field]);
}

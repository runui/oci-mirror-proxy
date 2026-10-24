import { copyFile, mkdir } from 'node:fs/promises';

const destination = new URL('../public/demos/', import.meta.url);
await mkdir(destination, { recursive: true });
for (const name of ['direct-mirror.gif', 'oci-mirror-proxy.gif', 'direct-mirror.cast', 'oci-mirror-proxy.cast']) {
  await copyFile(new URL(`../../../docs/demos/${name}`, import.meta.url), new URL(name, destination));
}
console.log('Prepared demo assets from docs/demos.');

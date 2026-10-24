import { copyFile, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const repositoryRoot = new URL('../../../', import.meta.url);
const publicDirectory = new URL('../public/', import.meta.url);

export async function repositoryAssets(root = repositoryRoot) {
  const assets = ['install.sh', 'install.ps1'].map((path) => ({ path, source: new URL(path, root) }));
  for (const name of ['direct-mirror.gif', 'oci-mirror-proxy.gif', 'direct-mirror.cast', 'oci-mirror-proxy.cast']) {
    assets.push({ path: `demos/${name}`, source: new URL(`docs/demos/${name}`, root) });
  }
  async function subscriptions(path) {
    const entries = await readdir(new URL(path, root), { withFileTypes: true });
    await Promise.all(entries.map(async (entry) => {
      const child = `${path}${entry.name}`;
      if (entry.isDirectory()) await subscriptions(`${child}/`);
      else if (entry.isFile()) assets.push({ path: child, source: new URL(child, root) });
      else throw new Error(`Unsupported subscription asset: ${child}`);
    }));
  }
  await subscriptions('subscriptions/');
  return assets;
}

export async function prepareAssets({ root = repositoryRoot, destination = publicDirectory } = {}) {
  const assets = await repositoryAssets(root);
  // This directory contains only generated copies; removed catalogs must not linger.
  await rm(new URL('subscriptions/', destination), { recursive: true, force: true });
  await Promise.all(assets.map(async ({ path, source }) => {
    const target = new URL(path, destination);
    await mkdir(dirname(fileURLToPath(target)), { recursive: true });
    if (path.startsWith('subscriptions/') && path.endsWith('.json')) {
      await writeFile(target, JSON.stringify(JSON.parse(await readFile(source, 'utf8'))));
    } else {
      await copyFile(source, target);
    }
  }));
  return assets.length;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const count = await prepareAssets();
  console.log(`Prepared ${count} repository assets: installers, subscriptions, and demos.`);
}

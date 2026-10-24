import { githubUrl, installCommand } from './shared';

const scriptUrl = `${githubUrl}/raw/refs/heads/master/install`;
const serviceCommand = `curl -fsSL ${scriptUrl}.sh | DOWNLOAD_BASE_URL=${githubUrl} sh -s -- --service`;

export const installPlatforms = [
  { id: 'linux', name: 'Linux', shell: 'sh', command: serviceCommand },
  { id: 'macos', name: 'macOS', shell: 'sh', command: serviceCommand },
  { id: 'windows', name: 'Windows', shell: 'PowerShell', command: `Invoke-WebRequest ${scriptUrl}.ps1 -OutFile install.ps1\n.\\install.ps1 -DownloadBaseUrl ${githubUrl}` },
  { id: 'freebsd', name: 'FreeBSD', shell: 'sh', command: installCommand },
] as const;

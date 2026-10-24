import { installCommand, installScriptUrl } from './shared';

const serviceCommand = `curl -fsSL ${installScriptUrl}.sh | sh -s -- --service`;

export const installPlatforms = [
  { id: 'linux', name: 'Linux', shell: 'sh', command: serviceCommand },
  { id: 'macos', name: 'macOS', shell: 'sh', command: serviceCommand },
  { id: 'windows', name: 'Windows', shell: 'PowerShell', command: `Invoke-WebRequest ${installScriptUrl}.ps1 -OutFile install.ps1\n.\\install.ps1` },
  { id: 'freebsd', name: 'FreeBSD', shell: 'sh', command: installCommand },
] as const;

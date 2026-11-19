export function detectPlatform(userAgent: string) {
	if (/Windows/i.test(userAgent)) return 'windows';
	if (/Macintosh|Mac OS X/i.test(userAgent) && !/iPhone|iPad|iPod/i.test(userAgent)) return 'macos';
	if (/FreeBSD/i.test(userAgent)) return 'freebsd';
	return 'linux';
}

export const platformBootstrap = `document.documentElement.dataset.installPlatform=(${detectPlatform.toString()})(navigator.userAgent);`;

export const platformSelection = `(()=>{
  const platform=document.documentElement.dataset.installPlatform||'linux';
  const input=document.getElementById('install-platform-'+platform);
  if(input){input.checked=true;input.defaultChecked=true;}
})();`;

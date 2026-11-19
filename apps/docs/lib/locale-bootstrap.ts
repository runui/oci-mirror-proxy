import { locales } from './locales.ts';
import { localeStorageKey } from './preferences.ts';

// Runs before the body is parsed; localized deep links are never negotiated.
export const localeBootstrap = `(()=>{
  if(location.pathname!=='/')return;
  const locales=${JSON.stringify(locales)};
  let saved;
  try{saved=localStorage.getItem(${JSON.stringify(localeStorageKey)})}catch{}
  let locale=locales.includes(saved)?saved:null;
  if(!locale){
    const languages=navigator.languages?.length?navigator.languages:[navigator.language];
    for(const language of languages){
      locale=locales.find(value=>new RegExp('^'+value.split('-')[0]+'(?:-|$)','i').test(language));
      if(locale)break;
    }
  }
  if(!locale||locale==='en')return;
  const style=document.createElement('style');
  style.textContent='html{visibility:hidden}';
  document.head.appendChild(style);
  try{location.replace('/'+locale+'/'+location.search+location.hash)}catch{style.remove()}
})();`;

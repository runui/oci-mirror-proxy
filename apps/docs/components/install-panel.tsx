'use client';

import Link from 'next/link';
import { ArrowUpRight, ShieldCheck } from 'lucide-react';
import { Tabs, TabsContent, TabsList, TabsTrigger } from 'fumadocs-ui/components/ui/tabs';
import { installPlatforms } from '@/lib/install';
import { homeContent } from '@/lib/home-content';
import { docsUrl, type Locale } from '@/lib/i18n';
import { CopyCommand } from './copy-command';

export function InstallPanel({ locale }: { locale: Locale }) {
  const text = homeContent[locale];

  return <div id="install" className="install-panel">
    <Tabs defaultValue="linux">
      <TabsList className="install-tabs" aria-label={text.platformLabel}>{installPlatforms.map(({ id, name }) => <TabsTrigger key={id} value={id}>{name}</TabsTrigger>)}</TabsList>
      {installPlatforms.map(({ id, shell, command }) => <TabsContent key={id} value={id} className="install-tab-panel">
        <div className="install-command framed">
          <div className="install-command-label"><span>{text.install}</span><span>{shell}</span></div>
          <div className="command-row"><span aria-hidden="true" className="terminal-prompt">{id === 'windows' ? '>' : '$'}</span><pre><code>{command}</code></pre><CopyCommand value={command} locale={locale} showLabel /></div>
        </div>
        <p className="install-instructions">{text.installNotes[id]}</p>
      </TabsContent>)}
    </Tabs>
    <div className="install-details"><span><ShieldCheck size={13} />{text.verified}</span><Link href={docsUrl(locale, 'installation')}>{text.installOptions}<ArrowUpRight size={13} /></Link></div>
  </div>;
}

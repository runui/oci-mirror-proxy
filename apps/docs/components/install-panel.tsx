import Link from 'next/link';
import { ArrowUpRight, ShieldCheck } from 'lucide-react';
import { installPlatforms } from '@/lib/install';
import { homeContent } from '@/lib/home-content';
import { docsUrl, type Locale } from '@/lib/i18n';
import { CopyCommand } from './copy-command';
import { platformSelection } from '@/lib/platform-bootstrap';

export function InstallPanel({ locale }: { locale: Locale }) {
  const text = homeContent[locale];

  return <div id="install" className="install-panel">
    <fieldset className="install-platforms">
      <legend className="sr-only">{text.platformLabel}</legend>
      {installPlatforms.map(({ id, name }) => <input key={id} className="install-platform-input sr-only" type="radio" name="install-platform" id={`install-platform-${id}`} value={id} aria-label={name} />)}
      <div className="install-tabs">{installPlatforms.map(({ id, name }) => <label key={id} htmlFor={`install-platform-${id}`}>{name}</label>)}</div>
      {installPlatforms.map(({ id, shell, command }) => <div key={id} data-platform={id} className="install-tab-panel">
        <div className="install-command framed">
          <div className="install-command-label"><span>{text.install}</span><span>{shell}</span></div>
          <div className="command-row"><span aria-hidden="true" className="terminal-prompt">{id === 'windows' ? '>' : '$'}</span><pre><code>{command}</code></pre><CopyCommand value={command} locale={locale} showLabel /></div>
        </div>
        <p className="install-instructions">{text.installNotes[id]}</p>
      </div>)}
    </fieldset>
    <script dangerouslySetInnerHTML={{ __html: platformSelection }} />
    <div className="install-details"><span><ShieldCheck size={13} />{text.verified}</span><Link href={docsUrl(locale, 'installation')}>{text.installOptions}<ArrowUpRight size={13} /></Link></div>
  </div>;
}

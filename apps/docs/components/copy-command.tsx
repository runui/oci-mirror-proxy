'use client';
import { useState } from 'react';
import { Check, Copy } from 'lucide-react';
import type { Locale } from '@/lib/i18n';

const copyText = {
  en: {
    idle: { label: 'Copy install command', text: 'Copy' },
    copied: { label: 'Copied', text: 'Copied' },
    error: { label: 'Copy failed; select the command to copy', text: 'Retry' },
  },
  'zh-CN': {
    idle: { label: '复制安装命令', text: '复制' },
    copied: { label: '已复制', text: '已复制' },
    error: { label: '复制失败，请选中命令手动复制', text: '重试' },
  },
};

export function CopyCommand({ value, locale, showLabel = false }: { value: string; locale: Locale; showLabel?: boolean }) {
  const [status, setStatus] = useState<'idle' | 'copied' | 'error'>('idle');
  const { label, text } = copyText[locale][status];
  return <button className="copy-command" aria-label={label} title={label} onClick={async () => {
    try { await navigator.clipboard.writeText(value); setStatus('copied'); }
    catch { setStatus('error'); }
  }}><span aria-live="polite" className="sr-only">{status !== 'idle' ? label : ''}</span>{status === 'copied' ? <Check size={16} /> : <Copy size={16} />}{showLabel && <span aria-hidden="true">{text}</span>}</button>;
}

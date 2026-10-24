'use client';
import { useState } from 'react';
import { Check, Copy } from 'lucide-react';
import type { Locale } from '@/lib/i18n';
import { localizedMessages } from '@/lib/messages';

const copyText = localizedMessages('copy');

export function CopyCommand({ value, locale, showLabel = false }: { value: string; locale: Locale; showLabel?: boolean }) {
	const [status, setStatus] = useState<'idle' | 'copied' | 'error'>('idle');
	const { label, text } = copyText[locale][status];
	return (
		<button
			className="copy-command"
			aria-label={label}
			title={label}
			onClick={async () => {
				try {
					await navigator.clipboard.writeText(value);
					setStatus('copied');
				} catch {
					setStatus('error');
				}
			}}>
			<span aria-live="polite" className="sr-only">
				{status !== 'idle' ? label : ''}
			</span>
			{status === 'copied' ? <Check size={16} /> : <Copy size={16} />}
			{showLabel && <span aria-hidden="true">{text}</span>}
		</button>
	);
}

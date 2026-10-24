import type { Metadata } from 'next';
import Link from 'next/link';
import '@fontsource-variable/geist';
import '@fontsource-variable/geist-mono';
import './global.css';

export const metadata: Metadata = { title: 'Page not found · oci-mirror-proxy' };

export default function NotFound() {
  return <html lang="en" className="dark"><body><main id="main-content" className="not-found"><p className="eyebrow">404 / NOT FOUND</p><h1>Nothing at this address.</h1><p>The page may have moved. Start with the documentation.</p><nav aria-label="Documentation"><Link href="/docs/">English documentation →</Link><Link href="/zh-CN/docs/" lang="zh-CN">简体中文文档 →</Link></nav></main></body></html>;
}

import { docsLlms } from '@/lib/source';

export const dynamic = 'force-static';
export async function GET() {
  return new Response(await docsLlms.index('zh-CN'), { headers: { 'Content-Type': 'text/plain; charset=utf-8' } });
}

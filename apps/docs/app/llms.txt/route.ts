import { docsLlms } from '@/lib/source';

export const revalidate = false;
export const dynamic = 'force-static';

export async function GET() {
  return new Response(await docsLlms.index('en'), { headers: { 'Content-Type': 'text/plain; charset=utf-8' } });
}

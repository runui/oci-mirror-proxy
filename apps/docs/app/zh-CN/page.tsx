import { HomePage } from '@/components/home-page';
import { homeMetadata } from '@/lib/metadata';

export const metadata = homeMetadata('zh-CN');
export default function Page() { return <HomePage locale="zh-CN" />; }

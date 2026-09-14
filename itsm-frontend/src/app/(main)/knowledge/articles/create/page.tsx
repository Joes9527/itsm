import { redirect } from 'next/navigation';

export default function LegacyArticleCreatePage() {
  redirect('/knowledge/articles/new');
}

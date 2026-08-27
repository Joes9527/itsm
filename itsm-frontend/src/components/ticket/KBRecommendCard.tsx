'use client';

import React, { useEffect, useState } from 'react';
import { BookOpen } from 'lucide-react';
import { KnowledgeBaseApi } from '@/lib/api/knowledge-base-api';
import type { KnowledgeArticle } from '@/types/knowledge-base';

interface KBRecommendCardProps {
  query: string;
}

/**
 * 工单详情右侧工具箱：推荐操作指引 (KB) 卡片。
 * 样式对齐 prototype：根据工单标题检索知识库，展示前 2 篇相关文章。
 */
export const KBRecommendCard: React.FC<KBRecommendCardProps> = ({ query }) => {
  const [articles, setArticles] = useState<KnowledgeArticle[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!query) {
      setLoading(false);
      return;
    }

    let cancelled = false;
    (async () => {
      try {
        const result = await KnowledgeBaseApi.search({ query });
        if (cancelled) return;
        setArticles((result?.articles ?? []).slice(0, 2).map(item => item.article));
      } catch {
        if (!cancelled) setArticles([]);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [query]);

  if (loading) return null;
  if (articles.length === 0) return null;

  return (
    <div className="bg-white rounded-2xl border border-slate-200/90 p-4 shadow-xs space-y-2.5 text-xs">
      <div className="flex items-center justify-between border-b border-slate-100 pb-2">
        <span className="font-bold text-slate-800 flex items-center gap-1.5 text-xs">
          <BookOpen size={13} className="text-slate-500" />
          推荐操作指引 (KB)
        </span>
        <span className="text-[11px] text-slate-400">{articles.length} 篇</span>
      </div>

      <div className="space-y-2">
        {articles.map(article => (
          <div
            key={article.id}
            className="p-2.5 bg-slate-50 hover:bg-orange-50/50 rounded-lg border border-slate-100 transition-colors cursor-pointer group"
          >
            <p className="text-[11px] font-medium text-slate-700 group-hover:text-orange-600 truncate m-0">
              {article.title}
            </p>
            <span className="text-[10px] text-slate-400 block mt-0.5">阅读量: {article.viewCount ?? 0} 次</span>
          </div>
        ))}
      </div>
    </div>
  );
};

export default KBRecommendCard;

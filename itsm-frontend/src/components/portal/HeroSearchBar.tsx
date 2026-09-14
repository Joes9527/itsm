'use client';

import React, { useState, useEffect, useRef } from 'react';
import { Input, Button, Spin, Tag } from 'antd';
import { Search, Sparkles, BookOpen, ExternalLink, ArrowRight, CheckCircle2 } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { httpClient } from '@/lib/api/http-client';
import { AIConfidenceBadge } from '@/components/ai/AIConfidenceBadge';

interface DeflectionResult {
  articles: Array<{
    id: number;
    title: string;
    snippet: string;
    score?: number;
    category?: string;
  }>;
  suggestedCatalogs: Array<{
    id: number;
    name: string;
    description: string;
    icon?: string;
  }>;
}

export const HeroSearchBar: React.FC = () => {
  const router = useRouter();
  const [query, setQuery] = useState('');
  const [loading, setLoading] = useState(false);
  const [results, setResults] = useState<DeflectionResult | null>(null);
  const [deflected, setDeflected] = useState(false);
  const debounceTimer = useRef<NodeJS.Timeout | null>(null);

  useEffect(() => {
    if (!query.trim() || query.length < 2) {
      setResults(null);
      setLoading(false);
      return;
    }

    if (debounceTimer.current) clearTimeout(debounceTimer.current);

    debounceTimer.current = setTimeout(async () => {
      setLoading(true);
      try {
        // 请求后端 RAG 与知识自愈搜索
        const res = await httpClient.get<any>(`/api/v1/knowledge/search?q=${encodeURIComponent(query)}&limit=3`);
        const articles = (res?.items || res?.articles || []).map((item: any) => ({
          id: item.id,
          title: item.title || item.name,
          snippet: item.content || item.summary || item.snippet || '',
          score: item.score || 0.88,
          category: item.category || '自愈指南',
        }));

        // 模拟/关联服务目录
        const catalogs = [];
        if (query.includes('VPN') || query.includes('网络') || query.includes('权限')) {
          catalogs.push({ id: 1, name: '申请 VPN / 远程办公访问权限', description: '适用于居家与出差办公访问内网系统' });
        }
        if (query.includes('Copilot') || query.includes('AI') || query.includes('软件')) {
          catalogs.push({ id: 2, name: '申请 Microsoft 365 Copilot 许可证', description: 'AI 办公副驾驶许可证采购与开通流程' });
        }

        setResults({ articles, suggestedCatalogs: catalogs });
      } catch (err) {
        console.error('Failed to search knowledge deflection:', err);
      } finally {
        setLoading(false);
      }
    }, 300);

    return () => {
      if (debounceTimer.current) clearTimeout(debounceTimer.current);
    };
  }, [query]);

  return (
    <div className="relative w-full max-w-3xl mx-auto my-8">
      {/* 搜索框 */}
      <div className="relative flex items-center bg-surface rounded-[8px] shadow-none transition-all border border-border p-2">
        <div className="pl-3 pr-2 text-foreground">
          <Search size={22} />
        </div>
        <input
          type="text"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setDeflected(false);
          }}
          placeholder="遇到了什么问题？搜索知识库或输入服务需求（如：申请 VPN、Copilot 许可证、重置密码）..."
          className="w-full bg-transparent border-none outline-none text-foreground placeholder-slate-400 text-[13px] px-2 py-1.5"
        />
        {loading && (
          <div className="pr-3">
            <Spin size="small" />
          </div>
        )}
      </div>

      {/* AI 推荐与自愈拦截 Panel (Deflection Card) */}
      {results && (results.articles.length > 0 || results.suggestedCatalogs.length > 0) && (
        <div className="absolute top-full left-0 right-0 mt-3 bg-surface rounded-[8px] shadow-none border border-border p-5 z-50 animate-in fade-in slide-in-from-top-2 duration-200">
          <div className="flex items-center justify-between pb-3 border-b border-border">
            <div className="flex items-center gap-2">
              <Sparkles size={16} className="text-foreground" />
              <span className="text-[15px] font-semibold text-foreground">
                AI 智能自愈与推荐建议
              </span>
            </div>
            <AIConfidenceBadge confidence={92} label="匹配度" />
          </div>

          {/* 知识库自愈文档推荐 */}
          {results.articles.length > 0 && (
            <div className="mt-3">
              <div className="text-[12px] font-medium text-muted uppercase tracking-wider mb-2">
                推荐自愈排障指南 (无需提单即可解决)
              </div>
              <div className="space-y-2">
                {results.articles.map((art) => (
                  <div
                    key={art.id}
                    onClick={() => router.push(`/knowledge/articles/${art.id}`)}
                    className="group p-3 rounded-[8px] bg-raised hover:bg-selected border border-border cursor-pointer transition-all flex items-start justify-between"
                  >
                    <div className="flex items-start gap-2.5">
                      <BookOpen size={16} className="text-foreground mt-0.5 group-hover:scale-110 transition-transform" />
                      <div>
                        <div className="text-[15px] font-semibold text-foreground group-hover:text-foreground">
                          {art.title}
                        </div>
                        <div className="text-[12px] text-muted line-clamp-1 mt-0.5">
                          {art.snippet || '点击查看排障详情与完整指引...'}
                        </div>
                      </div>
                    </div>
                    <ArrowRight size={14} className="text-muted group-hover:text-foreground group-hover:translate-x-0.5 transition-all mt-1" />
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* 关联服务目录 */}
          {results.suggestedCatalogs.length > 0 && (
            <div className="mt-4">
              <div className="text-[12px] font-medium text-muted uppercase tracking-wider mb-2">
                相关快捷服务申请
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                {results.suggestedCatalogs.map((cat) => (
                  <div
                    key={cat.id}
                    onClick={() => router.push(`/service-catalog`)}
                    className="p-3 rounded-[8px] bg-raised hover:bg-selected border border-border cursor-pointer transition-all"
                  >
                    <div className="text-[15px] font-semibold text-foreground">{cat.name}</div>
                    <div className="text-[12px] text-muted line-clamp-1 mt-0.5">{cat.description}</div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* 自愈反馈条 */}
          <div className="mt-4 pt-3 border-t border-border flex items-center justify-between text-[12px] text-muted">
            {deflected ? (
              <div className="flex items-center gap-1.5 text-emerald-600 font-medium">
                <CheckCircle2 size={15} />
                <span>很高兴能帮到您！已为您记录自愈成功。</span>
              </div>
            ) : (
              <>
                <span>是否已成功解决您的问题？</span>
                <div className="flex items-center gap-2">
                  <Button size="small" onClick={() => setDeflected(true)}>
                    🎉 解决了 (无需提单)
                  </Button>
                  <Button size="small" type="primary" onClick={() => router.push('/tickets/create')}>
                    仍需人工提单
                  </Button>
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
};

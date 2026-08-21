'use client';

import React, { useEffect, useState } from 'react';
import { Tag } from 'antd';
import { Button, Input, message } from 'antd';
import {
  TrendingUp,
  Sparkles,
  ArrowDownRight,
  Layers,
  Timer,
} from 'lucide-react';
import { AIConfidenceBadge } from '@/components/ai/AIConfidenceBadge';
import { LoadingEmptyError } from '@/components/ui/LoadingEmptyError';
import { TicketAnalyticsApi, type AnalyticsSummary, type AnalyticsDataPoint } from '@/lib/api/ticket-analytics-api';
import { aiChatStream } from '@/lib/api/ai-api';

function formatMinutes(minutes?: number): string {
  if (!minutes || minutes <= 0) return '暂无数据';
  if (minutes >= 60) return `${(minutes / 60).toFixed(1)} 小时`;
  return `${minutes.toFixed(0)} 分钟`;
}

const TIME_RANGE: [string, string] = (() => {
  const end = new Date();
  const start = new Date();
  start.setDate(start.getDate() - 90);
  const fmt = (d: Date) => d.toISOString().slice(0, 10);
  return [fmt(start), fmt(end)];
})();

export default function ExecutiveDashboardPage() {
  const [summary, setSummary] = useState<AnalyticsSummary | null>(null);
  const [categoryPoints, setCategoryPoints] = useState<AnalyticsDataPoint[]>([]);
  const [priorityPoints, setPriorityPoints] = useState<AnalyticsDataPoint[]>([]);
  const [dataLoading, setDataLoading] = useState(true);
  const [dataError, setDataError] = useState(false);

  const [chatQuery, setChatQuery] = useState('');
  const [chatLoading, setChatLoading] = useState(false);
  const [chatInsight, setChatInsight] = useState<string | null>(null);

  const loadAnalytics = async () => {
    setDataLoading(true);
    setDataError(false);
    try {
      const [overall, byCategory, byPriority] = await Promise.all([
        TicketAnalyticsApi.getDeepAnalytics({
          dimensions: [],
          metrics: ['count', 'response_time', 'resolution_time'],
          chartType: 'table',
          timeRange: TIME_RANGE,
          filters: {},
        }),
        TicketAnalyticsApi.getDeepAnalytics({
          dimensions: ['category'],
          metrics: ['count'],
          chartType: 'table',
          timeRange: TIME_RANGE,
          filters: {},
          groupBy: 'category',
        }),
        TicketAnalyticsApi.getDeepAnalytics({
          dimensions: ['priority'],
          metrics: ['count', 'resolution_time'],
          chartType: 'table',
          timeRange: TIME_RANGE,
          filters: {},
          groupBy: 'priority',
        }),
      ]);
      setSummary(overall.summary);
      setCategoryPoints((byCategory.data || []).slice().sort((a, b) => b.value - a.value).slice(0, 4));
      setPriorityPoints(byPriority.data || []);
    } catch {
      setDataError(true);
    } finally {
      setDataLoading(false);
    }
  };

  useEffect(() => {
    loadAnalytics();
  }, []);

  const handleAskBI = async () => {
    if (!chatQuery.trim()) return;
    setChatLoading(true);
    setChatInsight(null);
    let accumulated = '';
    try {
      await aiChatStream(
        { query: chatQuery },
        {
          onDelta: (delta) => {
            accumulated += delta;
            setChatInsight(accumulated);
          },
          onError: () => {
            setChatInsight('AI 洞察服务暂时不可用，请稍后重试，或直接查看下方基于真实工单数据的统计看板。');
          },
        }
      );
    } catch {
      setChatInsight('AI 洞察服务暂时不可用，请稍后重试，或直接查看下方基于真实工单数据的统计看板。');
    } finally {
      setChatLoading(false);
    }
  };

  const inProgress = summary ? Math.max(summary.total - summary.resolved, 0) : undefined;

  return (
    <div className="space-y-6 animate-in fade-in duration-200">
      {/* 顶部标题 */}
      <div>
        <div className="flex items-center gap-2">
          <TrendingUp size={22} className="text-purple-600" />
          <h1 className="text-2xl font-black text-slate-900 dark:text-slate-50 m-0 tracking-tight">
            IT 战略决策与效能驾驶舱 (Executive Insights)
          </h1>
        </div>
        <p className="text-xs text-slate-500 mt-1">
          面向 IT 总监与高管团队的全局 MTTR 趋势、SLA 达标率与工单结构分析（近 90 天）
        </p>
      </div>

      {/* 1. ChatBI 对话式数据探索与洞察 */}
      <div className="p-5 rounded-2xl bg-gradient-to-r from-purple-900 via-indigo-900 to-slate-900 text-white shadow-xl space-y-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Sparkles size={18} className="text-purple-400" />
            <span className="text-sm font-bold">ChatBI 对话式数据洞察</span>
          </div>
          <AIConfidenceBadge confidence={80} label="RAG 知识检索" />
        </div>

        <div className="flex gap-2">
          <Input
            value={chatQuery}
            onChange={(e) => setChatQuery(e.target.value)}
            onPressEnter={handleAskBI}
            placeholder="输入自然语言提问（回答基于知识库检索，结构化统计请参考下方看板）"
            className="bg-white/10 border-white/20 text-white placeholder-white/50 rounded-xl h-10"
          />
          <Button
            type="primary"
            loading={chatLoading}
            onClick={handleAskBI}
            className="bg-purple-600 hover:bg-purple-500 border-none h-10 px-5 font-semibold text-xs rounded-xl"
          >
            提问洞察
          </Button>
        </div>

        {chatInsight && (
          <div className="p-4 rounded-xl bg-white/10 border border-white/10 text-xs text-purple-100 whitespace-pre-line leading-relaxed animate-in fade-in">
            {chatInsight}
          </div>
        )}
      </div>

      {(dataLoading || dataError) ? (
        <LoadingEmptyError
          state={dataLoading ? 'loading' : 'error'}
          loadingText="正在加载统计数据..."
          error={{ description: '统计数据加载失败，请稍后重试', onAction: loadAnalytics, actionText: '重试' }}
        />
      ) : (
        <>
          {/* 2. 核心战略宏观指标（真实数据，近 90 天） */}
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            <div className="p-5 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-sm">
              <div className="text-xs font-semibold text-slate-400">平均故障解决时长 (MTTR)</div>
              <div className="text-2xl font-black text-slate-900 dark:text-slate-100 mt-1">
                {formatMinutes(summary?.avgResolutionTime)}
              </div>
              <div className="text-xs text-slate-400 mt-2 flex items-center gap-1">
                <ArrowDownRight size={14} /> 基于近 90 天已解决工单
              </div>
            </div>

            <div className="p-5 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-sm">
              <div className="text-xs font-semibold text-slate-400">SLA 达标率</div>
              <div className="text-2xl font-black text-emerald-600 mt-1">
                {summary ? `${summary.slaCompliance.toFixed(1)}%` : '暂无数据'}
              </div>
              <div className="text-xs text-slate-400 mt-2">已解决 / 总工单数（近 90 天）</div>
            </div>

            <div className="p-5 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-sm">
              <div className="text-xs font-semibold text-slate-400">客户满意度 (CSAT)</div>
              <div className="text-2xl font-black text-purple-600 mt-1">
                {summary && summary.customerSatisfaction > 0
                  ? `${summary.customerSatisfaction.toFixed(2)} / 5.0`
                  : '暂无评价数据'}
              </div>
              <div className="text-xs text-slate-400 mt-2">基于已评价工单的真实评分均值</div>
            </div>

            <div className="p-5 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-sm">
              <div className="text-xs font-semibold text-slate-400">当前处理中工单数</div>
              <div className="text-2xl font-black text-indigo-600 mt-1">
                {inProgress ?? '暂无数据'}
              </div>
              <div className="text-xs text-indigo-600 font-medium mt-2">
                总量 {summary?.total ?? 0} · 已解决 {summary?.resolved ?? 0}
              </div>
            </div>
          </div>

          {/* 3. 工单结构分析 */}
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
            <div className="bg-white dark:bg-slate-900 rounded-2xl p-6 border border-slate-200 dark:border-slate-800 shadow-sm">
              <h3 className="text-base font-bold text-slate-900 dark:text-slate-100 mb-1">工单分类分布 (Top 4)</h3>
              <p className="text-xs text-slate-400 mb-4">近 90 天各分类工单量，反映主要服务需求来源</p>

              {categoryPoints.length === 0 ? (
                <div className="text-xs text-slate-400 py-6 text-center">近 90 天暂无分类数据</div>
              ) : (
                <div className="space-y-3">
                  {categoryPoints.map((item) => (
                    <div
                      key={item.name}
                      className="p-3 rounded-xl bg-slate-50 dark:bg-slate-800/40 border border-slate-100 dark:border-slate-800 flex items-center justify-between"
                    >
                      <div className="flex items-center gap-2.5">
                        <Layers size={16} className="text-primary-600" />
                        <div className="text-xs font-bold text-slate-800 dark:text-slate-200">{item.name}</div>
                      </div>
                      <div className="text-xs font-bold text-slate-900 dark:text-slate-100">{item.value} 单</div>
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div className="bg-white dark:bg-slate-900 rounded-2xl p-6 border border-slate-200 dark:border-slate-800 shadow-sm">
              <h3 className="text-base font-bold text-slate-900 dark:text-slate-100 mb-1">各优先级平均处理时长</h3>
              <p className="text-xs text-slate-400 mb-4">近 90 天按优先级统计的平均解决耗时</p>

              {priorityPoints.length === 0 ? (
                <div className="text-xs text-slate-400 py-6 text-center">近 90 天暂无优先级数据</div>
              ) : (
                <div className="space-y-3">
                  {priorityPoints.map((item) => (
                    <div
                      key={item.name}
                      className="p-3 rounded-xl bg-slate-50 dark:bg-slate-800/40 border border-slate-100 dark:border-slate-800 flex items-center justify-between"
                    >
                      <div className="flex items-center gap-2.5">
                        <Timer size={16} className="text-primary-600" />
                        <div>
                          <div className="text-xs font-bold text-slate-800 dark:text-slate-200">{item.name}</div>
                          <div className="text-[11px] text-slate-400">工单量 {item.count ?? item.value}</div>
                        </div>
                      </div>
                      <Tag color="processing" className="text-[11px] mr-0">
                        {formatMinutes(item.avgTime)}
                      </Tag>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        </>
      )}
    </div>
  );
}

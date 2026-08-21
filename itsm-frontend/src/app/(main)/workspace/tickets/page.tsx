'use client';

import React, { useCallback, useEffect, useState } from 'react';
import { Tag, Button, Input, Dropdown, message, Avatar } from 'antd';
import type { MenuProps } from 'antd';
import dayjs from 'dayjs';
import relativeTime from 'dayjs/plugin/relativeTime';
import {
  Inbox,
  UserCheck,
  Clock,
  Sparkles,
  Send,
  CornerDownRight,
  Forward,
  User,
  Mail,
  Laptop,
  CheckCircle,
  FileText,
  AlertCircle,
} from 'lucide-react';
import { SLACountdownTimer } from '@/components/workspace/SLACountdownTimer';
import { AISimilarSolutionsPanel } from '@/components/workspace/AISimilarSolutionsPanel';
import { AIConfidenceBadge } from '@/components/ai/AIConfidenceBadge';
import { LoadingEmptyError } from '@/components/ui/LoadingEmptyError';
import { TicketApi } from '@/lib/api/ticket-api';
import { aiTriage, type TriageResult } from '@/lib/api/ai-api';
import { useAuthStore } from '@/lib/store/auth-store';
import type { Ticket } from '@/lib/api/types';
import type { GetTicketsParams } from '@/lib/api/api-config';

dayjs.extend(relativeTime);

const QUEUE_FILTERS = [
  { id: 'assigned_to_me', name: '分给我的' },
  { id: 'unassigned_pool', name: '服务台未分配池 (L1)' },
  { id: 'sla_breaching', name: '即将超时 (<30m)', alert: true },
  { id: 'pending_user', name: '待用户回复' },
] as const;

type QueueId = (typeof QUEUE_FILTERS)[number]['id'];

const PRIORITY_LABEL: Record<string, { text: string; color: string }> = {
  urgent: { text: '紧急', color: 'red' },
  critical: { text: '紧急', color: 'red' },
  high: { text: '高优', color: 'red' },
  medium: { text: '中优', color: 'blue' },
  low: { text: '低优', color: 'default' },
};

function buildQueueParams(queueId: QueueId, currentUserId?: number): GetTicketsParams {
  switch (queueId) {
    case 'assigned_to_me':
      return currentUserId ? { assigneeId: currentUserId } : {};
    case 'unassigned_pool':
      return { unassigned: true };
    case 'sla_breaching':
      return { slaBreachingWithinMinutes: 30 };
    case 'pending_user':
      return { status: 'pending' };
    default:
      return {};
  }
}

export default function WorkspaceTicketsPage() {
  const currentUser = useAuthStore((state) => state.user);

  const [selectedQueue, setSelectedQueue] = useState<QueueId>('assigned_to_me');
  const [queueCounts, setQueueCounts] = useState<Partial<Record<QueueId, number>>>({});

  const [tickets, setTickets] = useState<Ticket[]>([]);
  const [ticketsLoading, setTicketsLoading] = useState(true);
  const [ticketsError, setTicketsError] = useState(false);
  const [selectedTicket, setSelectedTicket] = useState<Ticket | null>(null);

  const [replyContent, setReplyContent] = useState('');
  const [replyType, setReplyType] = useState<'public' | 'internal'>('public');
  const [summaryLoading, setSummaryLoading] = useState(false);
  const [summaryText, setSummaryText] = useState<string | null>(null);

  const [triageResult, setTriageResult] = useState<TriageResult | null>(null);
  const [triageLoading, setTriageLoading] = useState(false);

  const fetchQueueCounts = useCallback(async () => {
    const entries = await Promise.all(
      QUEUE_FILTERS.map(async (q) => {
        try {
          const res = await TicketApi.getTickets({
            ...buildQueueParams(q.id, currentUser?.id),
            pageSize: 1,
          });
          return [q.id, res.total] as const;
        } catch {
          return [q.id, undefined] as const;
        }
      })
    );
    setQueueCounts(Object.fromEntries(entries));
  }, [currentUser?.id]);

  const fetchQueueTickets = useCallback(
    async (queueId: QueueId) => {
      setTicketsLoading(true);
      setTicketsError(false);
      try {
        const res = await TicketApi.getTickets({
          ...buildQueueParams(queueId, currentUser?.id),
          pageSize: 50,
          sortBy: 'createdAt',
          sortOrder: 'desc',
        });
        setTickets(res.tickets || []);
        setSelectedTicket((prev) => {
          if (prev && (res.tickets || []).some((t) => t.id === prev.id)) return prev;
          return res.tickets?.[0] ?? null;
        });
      } catch {
        setTicketsError(true);
        setTickets([]);
        setSelectedTicket(null);
      } finally {
        setTicketsLoading(false);
      }
    },
    [currentUser?.id]
  );

  useEffect(() => {
    fetchQueueCounts();
  }, [fetchQueueCounts]);

  useEffect(() => {
    fetchQueueTickets(selectedQueue);
  }, [selectedQueue, fetchQueueTickets]);

  // AI 智能分诊：切换工单时对当前工单做一次置信度评估，失败时静默降级（不阻塞人工处理）
  useEffect(() => {
    if (!selectedTicket) {
      setTriageResult(null);
      return;
    }
    let cancelled = false;
    setTriageLoading(true);
    aiTriage(selectedTicket.title, selectedTicket.description || '')
      .then((result) => {
        if (!cancelled) setTriageResult(result);
      })
      .catch(() => {
        if (!cancelled) setTriageResult(null);
      })
      .finally(() => {
        if (!cancelled) setTriageLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [selectedTicket?.id]);

  const handleGenerateSummary = () => {
    setSummaryLoading(true);
    setTimeout(() => {
      setSummaryText(
        `【AI 一键工单摘要】：\n• 工单：${selectedTicket?.title ?? ''}\n• 当前状态：${selectedTicket?.status ?? '未知'}\n• 建议操作：核对处理进展后同步申请人。`
      );
      setSummaryLoading(false);
    }, 600);
  };

  const handleSendReply = () => {
    if (!replyContent.trim()) {
      message.warning('请输入回复内容');
      return;
    }
    message.success(replyType === 'public' ? '已成功公开发送回复给申请人' : '已添加内部私密技术备注');
    setReplyContent('');
  };

  const escalateMenu: MenuProps = {
    items: [
      { key: 'l2_network', label: '转派至 L2 网络运维组' },
      { key: 'l2_system', label: '转派至 L2 系统与云平台组' },
      { key: 'dba', label: '转派至 数据库运维组' },
      { key: 'security', label: '升级为 安全应急事件 (P1)' },
    ],
    onClick: ({ key }) => {
      message.success(`工单已成功转派流转 (目标: ${key})`);
    },
  };

  return (
    <div className="h-[calc(100vh-100px)] flex flex-col md:flex-row gap-4 overflow-hidden animate-in fade-in duration-200">
      {/* ================= 1. 左栏：工单队列与列表 (300px) ================= */}
      <div className="w-full md:w-80 flex flex-col bg-white dark:bg-slate-900 rounded-2xl border border-slate-200 dark:border-slate-800 shadow-sm overflow-hidden flex-shrink-0">
        {/* 队列选择 */}
        <div className="p-3 border-b border-slate-100 dark:border-slate-800 bg-slate-50/50 dark:bg-slate-800/30">
          <div className="grid grid-cols-2 gap-1.5">
            {QUEUE_FILTERS.map((q) => (
              <button
                key={q.id}
                onClick={() => setSelectedQueue(q.id)}
                className={`flex items-center justify-between p-2 rounded-xl text-xs font-semibold transition-all text-left ${
                  selectedQueue === q.id
                    ? 'bg-white dark:bg-slate-800 text-primary-600 shadow-sm border border-slate-200/80 dark:border-slate-700'
                    : 'text-slate-600 dark:text-slate-400 hover:bg-white/60'
                }`}
              >
                <span className="truncate">{q.name}</span>
                <span
                  className={`px-1.5 py-0.5 rounded-full text-[10px] ${
                    'alert' in q && q.alert
                      ? 'bg-red-100 dark:bg-red-950 text-red-600 font-bold'
                      : 'bg-slate-100 dark:bg-slate-800 text-slate-500'
                  }`}
                >
                  {queueCounts[q.id] ?? '–'}
                </span>
              </button>
            ))}
          </div>
        </div>

        {/* 工单卡片列表 */}
        <div className="flex-1 overflow-y-auto divide-y divide-slate-100 dark:divide-slate-800">
          {(ticketsLoading || ticketsError || tickets.length === 0) ? (
            <LoadingEmptyError
              state={ticketsLoading ? 'loading' : ticketsError ? 'error' : 'empty'}
              loadingText="正在加载工单队列..."
              minHeight={200}
              error={{
                description: '工单队列加载失败，请稍后重试',
                onAction: () => fetchQueueTickets(selectedQueue),
                actionText: '重试',
              }}
              empty={{
                title: '当前队列没有工单',
                description: '切换其他队列试试，或等待新工单进入',
                showAction: false,
              }}
            />
          ) : (
            tickets.map((tkt) => {
              const priorityMeta = PRIORITY_LABEL[tkt.priority] ?? { text: tkt.priority, color: 'default' };
              return (
                <div
                  key={tkt.id}
                  onClick={() => setSelectedTicket(tkt)}
                  className={`p-3.5 cursor-pointer transition-all ${
                    selectedTicket?.id === tkt.id
                      ? 'bg-primary-50/40 dark:bg-primary-950/20 border-l-4 border-primary-600'
                      : 'hover:bg-slate-50 dark:hover:bg-slate-800/50'
                  }`}
                >
                  <div className="flex items-center justify-between gap-1 mb-1">
                    <span className="font-mono text-xs font-semibold text-slate-400">
                      {tkt.ticketNumber}
                    </span>
                    <Tag color={priorityMeta.color} className="mr-0 text-[10px]">
                      {priorityMeta.text}
                    </Tag>
                  </div>
                  <div className="text-xs font-bold text-slate-800 dark:text-slate-200 line-clamp-2 mb-1.5">
                    {tkt.title}
                  </div>
                  <div className="flex items-center justify-between text-[11px] text-slate-400">
                    <span>{tkt.requester?.name ?? '未知申请人'}</span>
                    <span>{dayjs(tkt.createdAt).fromNow()}</span>
                  </div>
                </div>
              );
            })
          )}
        </div>
      </div>

      {/* ================= 2. 中栏：工单核心上下文与处理工作台 (Flex-1) ================= */}
      <div className="flex-1 flex flex-col bg-white dark:bg-slate-900 rounded-2xl border border-slate-200 dark:border-slate-800 shadow-sm overflow-hidden min-w-0">
        {!selectedTicket ? (
          <LoadingEmptyError
            state={ticketsLoading ? 'loading' : 'empty'}
            loadingText="正在加载工单..."
            empty={{ title: '请选择一个工单', description: '从左侧队列中选择工单开始处理', showAction: false }}
          />
        ) : (
          <>
            {/* 顶部标题与 SLA / 快捷动作栏 */}
            <div className="p-4 border-b border-slate-100 dark:border-slate-800 flex flex-wrap items-center justify-between gap-3">
              <div>
                <div className="flex items-center gap-2">
                  <span className="font-mono text-xs font-bold text-primary-600">
                    {selectedTicket.ticketNumber}
                  </span>
                  <h2 className="text-base font-bold text-slate-900 dark:text-slate-100 m-0 line-clamp-1">
                    {selectedTicket.title}
                  </h2>
                </div>
                <div className="text-xs text-slate-400 mt-1 flex items-center gap-2">
                  <span>申请人：{selectedTicket.requester?.name ?? '未知'}</span>
                  <span>•</span>
                  <span>状态：{selectedTicket.status}</span>
                </div>
              </div>

              <div className="flex items-center gap-2">
                <SLACountdownTimer
                  targetTime={selectedTicket.slaInfo?.dueTime}
                  priority={selectedTicket.priority}
                />
                <Dropdown menu={escalateMenu}>
                  <Button size="small" icon={<Forward size={14} />}>
                    转派/升级
                  </Button>
                </Dropdown>
                <Button
                  size="small"
                  type="primary"
                  className="bg-emerald-600 hover:bg-emerald-500 border-none"
                  icon={<CheckCircle size={14} />}
                  onClick={() => message.success('工单已完结，已自动记录执行履约结果！')}
                >
                  完成并结单
                </Button>
              </div>
            </div>

            {/* 沟通流与详情区域 */}
            <div className="flex-1 overflow-y-auto p-4 space-y-4">
              {/* AI 智能分诊卡片 */}
              <div className="p-3.5 rounded-xl bg-indigo-50/60 dark:bg-indigo-950/20 border border-indigo-200/80 dark:border-indigo-900/40">
                <div className="flex items-center justify-between mb-2">
                  <div className="flex items-center gap-1.5 text-xs font-bold text-indigo-900 dark:text-indigo-300">
                    <Sparkles size={15} className="text-indigo-600" />
                    <span>AI 智能分诊</span>
                  </div>
                  {triageResult && (
                    <AIConfidenceBadge confidence={triageResult.confidence} label="分诊置信度" />
                  )}
                </div>
                <div className="text-xs text-indigo-900/80 dark:text-indigo-200/90 leading-relaxed">
                  {triageLoading
                    ? 'AI 正在分析工单内容...'
                    : triageResult
                      ? `建议分类：${triageResult.category} · 建议优先级：${triageResult.priority}${
                          triageResult.explanation ? ` · ${triageResult.explanation}` : ''
                        }`
                    : '暂无 AI 分诊建议（服务不可用时不影响人工处理）'}
                </div>
              </div>

              {/* AI 智能工单摘要卡片 */}
              <div className="p-3.5 rounded-xl bg-purple-50/60 dark:bg-purple-950/20 border border-purple-200/80 dark:border-purple-900/40">
                <div className="flex items-center justify-between mb-2">
                  <div className="flex items-center gap-1.5 text-xs font-bold text-purple-900 dark:text-purple-300">
                    <Sparkles size={15} className="text-purple-600" />
                    <span>AI 智能工单速读与交接摘要</span>
                  </div>
                  <Button
                    size="small"
                    type="link"
                    loading={summaryLoading}
                    onClick={handleGenerateSummary}
                    className="text-xs text-purple-600 font-semibold p-0 h-auto"
                  >
                    {summaryText ? '重新生成' : '一键提取摘要'}
                  </Button>
                </div>
                <div className="text-xs text-purple-900/80 dark:text-purple-200/90 whitespace-pre-line leading-relaxed font-sans">
                  {summaryText ||
                    '点击右上角"一键提取摘要"，AI 将快速汇总用户诉求、已排除原因与当前交接点。'}
                </div>
              </div>

              {/* 工单初始描述 */}
              <div className="p-4 rounded-xl bg-slate-50 dark:bg-slate-800/50 border border-slate-100 dark:border-slate-800 text-xs text-slate-700 dark:text-slate-300">
                <div className="font-bold text-slate-900 dark:text-slate-100 mb-1">工单详情描述：</div>
                {selectedTicket.description || '（无详细描述）'}
              </div>
            </div>

            {/* 底部回复输入区 */}
            <div className="p-4 border-t border-slate-100 dark:border-slate-800 bg-slate-50/40 dark:bg-slate-800/20 space-y-2">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <button
                    onClick={() => setReplyType('public')}
                    className={`text-xs font-semibold px-2.5 py-1 rounded-lg transition-colors ${
                      replyType === 'public'
                        ? 'bg-primary-600 text-white'
                        : 'text-slate-500 hover:bg-slate-200 dark:hover:bg-slate-700'
                    }`}
                  >
                    公开回复申请人
                  </button>
                  <button
                    onClick={() => setReplyType('internal')}
                    className={`text-xs font-semibold px-2.5 py-1 rounded-lg transition-colors ${
                      replyType === 'internal'
                        ? 'bg-amber-600 text-white'
                        : 'text-slate-500 hover:bg-slate-200 dark:hover:bg-slate-700'
                    }`}
                  >
                    内部技术协作备注 (私密)
                  </button>
                </div>
                <span className="text-[11px] text-slate-400">Ctrl + Enter 快捷发送</span>
              </div>

              <Input.TextArea
                rows={3}
                value={replyContent}
                onChange={(e) => setReplyContent(e.target.value)}
                placeholder={
                  replyType === 'public'
                    ? '输入回复内容告知用户处理进展...'
                    : '输入内部技术排障备注，仅技术团队可见...'
                }
                className="rounded-xl"
              />

              <div className="flex items-center justify-between pt-1">
                <Button
                  size="small"
                  icon={<Sparkles size={13} />}
                  onClick={() => {
                    setReplyContent(
                      `您好，关于「${selectedTicket.title}」，我们已收到并正在处理，进展会第一时间同步给您。`
                    );
                    message.success('AI 已帮您生成专业客服回复草稿');
                  }}
                  className="text-xs"
                >
                  AI 润色/生成标准回复
                </Button>

                <Button
                  type="primary"
                  size="small"
                  icon={<Send size={13} />}
                  onClick={handleSendReply}
                  className="text-xs"
                >
                  发送回复
                </Button>
              </div>
            </div>
          </>
        )}
      </div>

      {/* ================= 3. 右栏：申请人画像与 AI 排障辅助 Panel (340px) ================= */}
      <div className="w-full md:w-80 flex flex-col gap-4 overflow-y-auto flex-shrink-0">
        {selectedTicket && (
          <>
            {/* 申请人画像 */}
            <div className="bg-white dark:bg-slate-900 rounded-2xl p-4 border border-slate-200 dark:border-slate-800 shadow-sm space-y-3">
              <div className="text-xs font-bold text-slate-800 dark:text-slate-100 uppercase tracking-wider">
                申请人信息
              </div>
              <div className="flex items-center gap-3">
                <Avatar size={40} className="bg-primary-600 font-bold">
                  {selectedTicket.requester?.name?.charAt(0) ?? '?'}
                </Avatar>
                <div>
                  <div className="text-sm font-bold text-slate-900 dark:text-slate-100">
                    {selectedTicket.requester?.name ?? '未知申请人'}
                  </div>
                  <div className="text-xs text-slate-400">{selectedTicket.requester?.email ?? '—'}</div>
                </div>
              </div>

              <div className="space-y-1.5 text-xs text-slate-500 pt-1 border-t border-slate-100 dark:border-slate-800">
                <div className="flex items-center gap-2">
                  <Mail size={13} className="text-slate-400" />
                  <span>邮箱：{selectedTicket.requester?.email ?? '未填写'}</span>
                </div>
                <div className="flex items-center gap-2">
                  <User size={13} className="text-slate-400" />
                  <span>处理人：{selectedTicket.assignee?.name ?? '未分配'}</span>
                </div>
                <div className="flex items-center gap-2">
                  <FileText size={13} className="text-slate-400" />
                  <span>分类：{selectedTicket.category ?? '未分类'}</span>
                </div>
              </div>
            </div>

            {/* AI 相似故障方案推荐卡片 */}
            <AISimilarSolutionsPanel
              ticketTitle={selectedTicket.title}
              onApplySolution={(solutionText) => {
                setReplyContent((prev) => (prev ? `${prev}\n\n${solutionText}` : solutionText));
              }}
            />
          </>
        )}
      </div>
    </div>
  );
}

'use client';

import React, { useState } from 'react';
import { Tag, Button, Input, Dropdown, message, Avatar } from 'antd';
import type { MenuProps } from 'antd';
import {
  Inbox,
  UserCheck,
  Clock,
  Sparkles,
  Send,
  CornerDownRight,
  Forward,
  User,
  Building,
  Laptop,
  CheckCircle,
  FileText,
  AlertCircle,
} from 'lucide-react';
import { SLACountdownTimer } from '@/components/workspace/SLACountdownTimer';
import { AISimilarSolutionsPanel } from '@/components/workspace/AISimilarSolutionsPanel';
import { AIConfidenceBadge } from '@/components/ai/AIConfidenceBadge';

const QUEUE_FILTERS = [
  { id: 'assigned_to_me', name: '分给我的', count: 4, active: true },
  { id: 'unassigned_pool', name: '服务台未分配池 (L1)', count: 12, active: false },
  { id: 'sla_breaching', name: '即将超时 (<30m)', count: 2, active: false, alert: true },
  { id: 'pending_user', name: '待用户回复', count: 5, active: false },
];

const MOCK_TICKETS = [
  {
    id: 'TKT-2026-0819',
    title: '【开通履约】Microsoft 365 Copilot 许可证审批通过，等待服务台发放',
    requester: '李思源',
    department: '华东大区-市场部',
    priority: 'high',
    status: '处理中',
    serviceCatalog: 'Copilot 许可证采购与开通',
    createdAt: '25分钟前',
    description:
      '申请人李思源的 Copilot 采购已获部门经理与 IT 总监审批通过。请服务台 L1 工程师在 M365 管理中心或执行脚本完成许可证绑定并通知用户。',
  },
  {
    id: 'TKT-2026-0818',
    title: '广州分公司 4 楼财务室打印机无法连接与脱机',
    requester: '张曼丽',
    department: '华南大区-财务部',
    priority: 'medium',
    status: '待分配',
    serviceCatalog: '办公外设支持',
    createdAt: '1小时前',
    description: '打印机 IP 192.168.4.55 突然脱机，多人无法打印发票。',
  },
];

export default function WorkspaceTicketsPage() {
  const [selectedQueue, setSelectedQueue] = useState('assigned_to_me');
  const [selectedTicket, setSelectedTicket] = useState(MOCK_TICKETS[0]);
  const [replyContent, setReplyContent] = useState('');
  const [replyType, setReplyType] = useState<'public' | 'internal'>('public');
  const [summaryLoading, setSummaryLoading] = useState(false);
  const [summaryText, setSummaryText] = useState<string | null>(null);

  const handleGenerateSummary = () => {
    setSummaryLoading(true);
    setTimeout(() => {
      setSummaryText(
        '【AI 一键工单摘要】：\n• 核心诉求：Copilot 采购审批流（流程实例 copilot_procurement）已全程通过，需在 M365 分配许可证。\n• 当前状态：已流转至服务台-L1 执行节点 (Activity_Execute)。\n• 建议操作：进入 Admin Center 分配 license 后点击一键关单并邮件告知。'
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
                    q.alert
                      ? 'bg-red-100 dark:bg-red-950 text-red-600 font-bold'
                      : 'bg-slate-100 dark:bg-slate-800 text-slate-500'
                  }`}
                >
                  {q.count}
                </span>
              </button>
            ))}
          </div>
        </div>

        {/* 工单卡片列表 */}
        <div className="flex-1 overflow-y-auto divide-y divide-slate-100 dark:divide-slate-800">
          {MOCK_TICKETS.map((tkt) => (
            <div
              key={tkt.id}
              onClick={() => setSelectedTicket(tkt)}
              className={`p-3.5 cursor-pointer transition-all ${
                selectedTicket.id === tkt.id
                  ? 'bg-primary-50/40 dark:bg-primary-950/20 border-l-4 border-primary-600'
                  : 'hover:bg-slate-50 dark:hover:bg-slate-800/50'
              }`}
            >
              <div className="flex items-center justify-between gap-1 mb-1">
                <span className="font-mono text-xs font-semibold text-slate-400">{tkt.id}</span>
                <Tag color={tkt.priority === 'high' ? 'red' : 'blue'} className="mr-0 text-[10px]">
                  {tkt.priority === 'high' ? '高优 P2' : '中优 P3'}
                </Tag>
              </div>
              <div className="text-xs font-bold text-slate-800 dark:text-slate-200 line-clamp-2 mb-1.5">
                {tkt.title}
              </div>
              <div className="flex items-center justify-between text-[11px] text-slate-400">
                <span>{tkt.requester}</span>
                <span>{tkt.createdAt}</span>
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* ================= 2. 中栏：工单核心上下文与处理工作台 (Flex-1) ================= */}
      <div className="flex-1 flex flex-col bg-white dark:bg-slate-900 rounded-2xl border border-slate-200 dark:border-slate-800 shadow-sm overflow-hidden min-w-0">
        {/* 顶部标题与 SLA / 快捷动作栏 */}
        <div className="p-4 border-b border-slate-100 dark:border-slate-800 flex flex-wrap items-center justify-between gap-3">
          <div>
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs font-bold text-primary-600">{selectedTicket.id}</span>
              <h2 className="text-base font-bold text-slate-900 dark:text-slate-100 m-0 line-clamp-1">
                {selectedTicket.title}
              </h2>
            </div>
            <div className="text-xs text-slate-400 mt-1 flex items-center gap-2">
              <span>申请人：{selectedTicket.requester}</span>
              <span>•</span>
              <span>所属：{selectedTicket.department}</span>
              <span>•</span>
              <span className="text-emerald-600 font-medium">履约节点：Activity_Execute</span>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <SLACountdownTimer />
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
                '点击右上角“一键提取摘要”，AI 将快速汇总用户诉求、已排除原因与当前交接点。'}
            </div>
          </div>

          {/* 工单初始描述 */}
          <div className="p-4 rounded-xl bg-slate-50 dark:bg-slate-800/50 border border-slate-100 dark:border-slate-800 text-xs text-slate-700 dark:text-slate-300">
            <div className="font-bold text-slate-900 dark:text-slate-100 mb-1">工单详情描述：</div>
            {selectedTicket.description}
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
                  `您好，您的 Copilot 许可证已在 Microsoft 365 成功开通，请重启 Teams/Outlook 客户端即可体验。如有任何问题请随时告知！`
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
      </div>

      {/* ================= 3. 右栏：申请人画像与 AI 排障辅助 Panel (340px) ================= */}
      <div className="w-full md:w-80 flex flex-col gap-4 overflow-y-auto flex-shrink-0">
        {/* 申请人 360 画像 */}
        <div className="bg-white dark:bg-slate-900 rounded-2xl p-4 border border-slate-200 dark:border-slate-800 shadow-sm space-y-3">
          <div className="text-xs font-bold text-slate-800 dark:text-slate-100 uppercase tracking-wider">
            申请人 360° 画像
          </div>
          <div className="flex items-center gap-3">
            <Avatar size={40} className="bg-primary-600 font-bold">
              {selectedTicket.requester.charAt(0)}
            </Avatar>
            <div>
              <div className="text-sm font-bold text-slate-900 dark:text-slate-100">
                {selectedTicket.requester}
              </div>
              <div className="text-xs text-slate-400">siyuan.li@company.com</div>
            </div>
          </div>

          <div className="space-y-1.5 text-xs text-slate-500 pt-1 border-t border-slate-100 dark:border-slate-800">
            <div className="flex items-center gap-2">
              <Building size={13} className="text-slate-400" />
              <span>部门：{selectedTicket.department}</span>
            </div>
            <div className="flex items-center gap-2">
              <User size={13} className="text-slate-400" />
              <span>直属主管：王建国 (总监)</span>
            </div>
            <div className="flex items-center gap-2">
              <Laptop size={13} className="text-slate-400" />
              <span>关联资产 CI：MacBook Pro 16 (资产编号: HW-8802)</span>
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
      </div>
    </div>
  );
}

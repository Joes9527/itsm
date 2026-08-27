'use client';

import React, { useState } from 'react';
import { Card, Button, Tag, Progress, message, Avatar } from 'antd';
import {
  Activity,
  AlertTriangle,
  Users,
  Clock,
  Sparkles,
  Flame,
  UserCheck,
  ArrowUpRight,
  ShieldAlert,
  Send,
} from 'lucide-react';
import { AIConfidenceBadge } from '@/components/ai/AIConfidenceBadge';

const MOCK_TEAM_MEMBERS = [
  { id: 1, name: '陈思宇 (L1客服)', activeTickets: 3, status: '在线', max: 8, avatar: '陈' },
  { id: 2, name: '林立峰 (L1客服)', activeTickets: 2, status: '在线', max: 8, avatar: '林', isLightest: true },
  { id: 3, name: '周小薇 (L2网络)', activeTickets: 6, status: '忙碌', max: 8, avatar: '周' },
  { id: 4, name: '黄志强 (L2系统)', activeTickets: 7, status: '重载', max: 8, avatar: '黄' },
];

export default function ManagerLiveBoardPage() {
  const [clusteringDismissed, setClusteringDismissed] = useState(false);
  const [dispatchLoading, setDispatchLoading] = useState(false);

  const handleAutoDispatch = () => {
    setDispatchLoading(true);
    setTimeout(() => {
      message.success('已联动 TeamWorkloadResolver：自动将未分配工单派发给当前最闲成员【林立峰】');
      setDispatchLoading(false);
    }, 600);
  };

  return (
    <div className="space-y-6 animate-in fade-in duration-200">
      {/* 顶部标题与派单调度按钮 */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <div className="flex items-center gap-2">
            <Activity size={22} className="text-amber-500" />
            <h1 className="text-2xl font-black text-slate-900 dark:text-slate-50 m-0 tracking-tight">
              服务台实时运营监控墙 (Operations Wallboard)
            </h1>
          </div>
          <p className="text-xs text-slate-500 mt-1">
            监控团队负载均衡态势、重大事件风险预警与流程卡壳节点
          </p>
        </div>

        <div className="flex items-center gap-2">
          <Button
            type="primary"
            icon={<Sparkles size={15} />}
            loading={dispatchLoading}
            onClick={handleAutoDispatch}
            className="bg-amber-600 hover:bg-amber-500 border-none font-semibold text-xs h-9 shadow-sm"
          >
            一键智能均衡派单 (TeamWorkloadResolver)
          </Button>
        </div>
      </div>

      {/* 1. AI 突发群体性故障告警 Banner */}
      {!clusteringDismissed && (
        <div className="p-4 rounded-2xl bg-red-50/80 dark:bg-red-950/30 border border-red-200 dark:border-red-900/60 shadow-sm flex flex-col md:flex-row md:items-center justify-between gap-4 animate-bounce-short">
          <div className="flex items-start gap-3">
            <div className="p-2 rounded-xl bg-red-100 dark:bg-red-900/60 text-red-600 dark:text-red-300 mt-0.5">
              <Flame size={20} />
            </div>
            <div>
              <div className="flex items-center gap-2">
                <span className="text-sm font-bold text-red-900 dark:text-red-200">
                  ⚠️ AI 聚类预警：检测到疑似华东区域 Exchange / 邮件服务器突发中断
                </span>
                <AIConfidenceBadge confidence={96} label="置信度" />
              </div>
              <p className="text-xs text-red-700 dark:text-red-300/80 mt-1 m-0">
                最近 15 分钟内突然涌入 8 张相似工单（“Outlook 无法同步”、“邮件发送失败”），建议合并为重大事件 (Major Incident) 并触发全员通知广播排查。
              </p>
            </div>
          </div>

          <div className="flex items-center gap-2 self-end md:self-center flex-shrink-0">
            <Button
              size="small"
              onClick={() => setClusteringDismissed(true)}
              className="text-xs"
            >
              忽略
            </Button>
            <Button
              size="small"
              danger
              type="primary"
              onClick={() => {
                message.success('已自动将 8 张关联工单合并为 P1 重大事件，已通知二线系统组与 IT 总监！');
                setClusteringDismissed(true);
              }}
              className="text-xs"
            >
              一键合并重大事件 (P1)
            </Button>
          </div>
        </div>
      )}

      {/* 2. 核心 KPI 数据卡片 */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <div className="p-5 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-sm">
          <div className="text-xs font-semibold text-slate-400">今日进单量 / 解决量</div>
          <div className="text-2xl font-black text-slate-900 dark:text-slate-100 mt-1 flex items-baseline gap-2">
            <span>42</span>
            <span className="text-sm font-normal text-slate-400">/ 38 已解决</span>
          </div>
          <div className="text-xs text-emerald-600 font-medium mt-2 flex items-center gap-1">
            <ArrowUpRight size={14} /> 结单率 90.4%
          </div>
        </div>

        <div className="p-5 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-sm">
          <div className="text-xs font-semibold text-slate-400">当前 SLA 达成率</div>
          <div className="text-2xl font-black text-emerald-600 mt-1">98.2%</div>
          <div className="text-xs text-slate-400 mt-2">月度目标: ≥95.0%</div>
        </div>

        <div className="p-5 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-sm">
          <div className="text-xs font-semibold text-slate-400">平均首次响应时长 (FCR)</div>
          <div className="text-2xl font-black text-primary-600 mt-1">8.5 分钟</div>
          <div className="text-xs text-emerald-600 font-medium mt-2">较昨日提速 12%</div>
        </div>

        <div className="p-5 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-sm">
          <div className="text-xs font-semibold text-slate-400">待分配工单池 (L1)</div>
          <div className="text-2xl font-black text-amber-500 mt-1">12 张</div>
          <div className="text-xs text-amber-600 font-medium mt-2">建议触发负载均衡派发</div>
        </div>
      </div>

      {/* 3. 团队成员工作负载均衡监控 */}
      <div className="bg-white dark:bg-slate-900 rounded-2xl p-6 border border-slate-200 dark:border-slate-800 shadow-sm">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="text-base font-bold text-slate-900 dark:text-slate-100 m-0">
              服务台-L1 / L2 团队工作负载看板 (Team Workload)
            </h3>
            <p className="text-xs text-slate-400 mt-0.5">
              实时聚合各成员手头上的 ActiveTickets，由智能分配算法挑最闲人员派单
            </p>
          </div>
          <Tag color="blue">当前在岗: 4 人</Tag>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
          {MOCK_TEAM_MEMBERS.map((mem) => (
            <div
              key={mem.id}
              className={`p-4 rounded-xl border transition-all ${
                mem.isLightest
                  ? 'bg-emerald-50/40 dark:bg-emerald-950/20 border-emerald-300 dark:border-emerald-800 shadow-sm'
                  : 'bg-slate-50 dark:bg-slate-800/40 border-slate-200/80 dark:border-slate-800'
              }`}
            >
              <div className="flex items-center justify-between mb-2">
                <div className="flex items-center gap-2">
                  <Avatar size={32} className={mem.isLightest ? 'bg-emerald-600' : 'bg-slate-600'}>
                    {mem.avatar}
                  </Avatar>
                  <div>
                    <div className="text-xs font-bold text-slate-800 dark:text-slate-100">{mem.name}</div>
                    <div className="text-[11px] text-slate-400">{mem.status}</div>
                  </div>
                </div>
                {mem.isLightest && (
                  <span className="text-[10px] bg-emerald-100 dark:bg-emerald-900/60 text-emerald-700 dark:text-emerald-300 font-bold px-1.5 py-0.5 rounded">
                    最闲待命
                  </span>
                )}
              </div>

              <div className="space-y-1">
                <div className="flex items-center justify-between text-xs text-slate-500">
                  <span>在办工单：{mem.activeTickets} / {mem.max}</span>
                  <span className="font-semibold">{Math.round((mem.activeTickets / mem.max) * 100)}%</span>
                </div>
                <Progress
                  percent={Math.round((mem.activeTickets / mem.max) * 100)}
                  size="small"
                  status={mem.activeTickets >= 7 ? 'exception' : 'normal'}
                  strokeColor={mem.isLightest ? '#10b981' : undefined}
                />
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

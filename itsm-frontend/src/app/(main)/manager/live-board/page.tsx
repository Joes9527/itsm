'use client';

import React, { useCallback, useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { Button, Tag, Progress, message, Avatar } from 'antd';
import {
  Activity,
  Users,
  Sparkles,
  Flame,
  ArrowUpRight,
} from 'lucide-react';
import { LoadingEmptyError } from '@/components/ui/LoadingEmptyError';
import { TicketApi } from '@/lib/api/ticket-api';
import { SLAApi } from '@/lib/api/sla-api';
import { TicketAnalyticsApi, type AnalyticsDataPoint } from '@/lib/api/ticket-analytics-api';

// 团队负载看板展示上限：容量条按此值渲染为满格，仅用于可视化，不代表真实排班上限
const WORKLOAD_DISPLAY_CAP = 8;
// 近 15 分钟新增工单数达到该阈值时，认为存在潜在批量故障，展示预警条
const SPIKE_ALERT_THRESHOLD = 5;

const WIDE_TIME_RANGE: [string, string] = (() => {
  const end = new Date();
  const start = new Date();
  start.setFullYear(start.getFullYear() - 1);
  const fmt = (d: Date) => d.toISOString().slice(0, 10);
  return [fmt(start), fmt(end)];
})();

function formatMinutes(minutes?: number): string {
  if (!minutes || minutes <= 0) return '暂无数据';
  if (minutes >= 60) return `${(minutes / 60).toFixed(1)} 小时`;
  return `${minutes.toFixed(0)} 分钟`;
}

export default function ManagerLiveBoardPage() {
  const router = useRouter();

  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [dispatchLoading, setDispatchLoading] = useState(false);
  const [spikeDismissed, setSpikeDismissed] = useState(false);

  const [todayNew, setTodayNew] = useState<number | null>(null);
  const [unassignedCount, setUnassignedCount] = useState<number | null>(null);
  const [slaCompliance, setSlaCompliance] = useState<number | null>(null);
  const [avgResponseTime, setAvgResponseTime] = useState<number | null>(null);
  const [recentSpikeCount, setRecentSpikeCount] = useState(0);
  const [teamWorkload, setTeamWorkload] = useState<AnalyticsDataPoint[]>([]);

  const loadBoard = useCallback(async () => {
    setLoading(true);
    setLoadError(false);
    try {
      const todayStart = new Date();
      todayStart.setHours(0, 0, 0, 0);
      const fifteenMinAgo = new Date(Date.now() - 15 * 60 * 1000);

      const [todayRes, unassignedRes, monitoring, workloadRes, spikeRes] = await Promise.all([
        TicketApi.getTickets({ pageSize: 1, dateFrom: todayStart.toISOString() }),
        TicketApi.getTickets({ unassigned: true, pageSize: 1 }),
        SLAApi.getSLAMonitoring(),
        TicketAnalyticsApi.getDeepAnalytics({
          dimensions: ['assignee'],
          metrics: ['count'],
          chartType: 'table',
          timeRange: WIDE_TIME_RANGE,
          filters: { status: 'in_progress' },
          groupBy: 'assignee',
        }),
        TicketApi.getTickets({ pageSize: 1, dateFrom: fifteenMinAgo.toISOString() }),
      ]);

      setTodayNew(todayRes.total);
      setUnassignedCount(unassignedRes.total);
      setSlaCompliance(monitoring.complianceRate);
      setAvgResponseTime(monitoring.averageResponseTime);
      setRecentSpikeCount(spikeRes.total);
      setTeamWorkload(
        (workloadRes.data || [])
          .filter((p) => p.name !== '未分配')
          .sort((a, b) => b.value - a.value)
          .slice(0, 6)
      );
    } catch {
      setLoadError(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadBoard();
  }, [loadBoard]);

  const handleAutoDispatch = async () => {
    if (teamWorkload.length === 0) {
      message.warning('暂无可用团队成员负载数据，无法执行自动派单');
      return;
    }
    setDispatchLoading(true);
    try {
      const lightest = teamWorkload.reduce((min, cur) => (cur.value < min.value ? cur : min), teamWorkload[0]);
      const pending = await TicketApi.getTickets({
        unassigned: true,
        pageSize: 1,
        sortBy: 'createdAt',
        sortOrder: 'asc',
      });
      const ticket = pending.tickets?.[0];
      if (!ticket) {
        message.info('当前没有待分配工单');
        return;
      }
      // teamWorkload 的 name 已由后端解析为真实姓名，用于用户提示；
      // 实际写入仍需 assigneeId，这里从工单负载分组的原始用户维度无法直接拿到 id，
      // 因此仅提示建议人选，具体指派仍需人工在工单详情中确认并分配。
      message.success(`建议将工单 ${ticket.ticketNumber} 优先派发给当前负载最低的 ${lightest.name}，请在工单详情中确认分配`);
      router.push(`/tickets`);
    } catch {
      message.error('自动派单建议生成失败，请稍后重试');
    } finally {
      setDispatchLoading(false);
    }
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
          <p className="text-xs text-slate-500 mt-1">监控团队负载均衡态势与待分配工单积压</p>
        </div>

        <div className="flex items-center gap-2">
          <Button
            type="primary"
            icon={<Sparkles size={15} />}
            loading={dispatchLoading}
            onClick={handleAutoDispatch}
            className="bg-amber-600 hover:bg-amber-500 border-none font-semibold text-xs h-9 shadow-sm"
          >
            智能推荐派单人选
          </Button>
        </div>
      </div>

      {/* 1. 进单量激增预警 Banner（基于真实近 15 分钟新增工单数） */}
      {!spikeDismissed && recentSpikeCount >= SPIKE_ALERT_THRESHOLD && (
        <div className="p-4 rounded-2xl bg-red-50/80 dark:bg-red-950/30 border border-red-200 dark:border-red-900/60 shadow-sm flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div className="flex items-start gap-3">
            <div className="p-2 rounded-xl bg-red-100 dark:bg-red-900/60 text-red-600 dark:text-red-300 mt-0.5">
              <Flame size={20} />
            </div>
            <div>
              <span className="text-sm font-bold text-red-900 dark:text-red-200">
                ⚠️ 进单量激增：近 15 分钟内新增 {recentSpikeCount} 张工单
              </span>
              <p className="text-xs text-red-700 dark:text-red-300/80 mt-1 m-0">
                短时间内工单量明显高于常态，建议人工核查是否存在批量性故障，并评估是否需要合并为重大事件处理。
              </p>
            </div>
          </div>

          <div className="flex items-center gap-2 self-end md:self-center flex-shrink-0">
            <Button size="small" onClick={() => setSpikeDismissed(true)} className="text-xs">
              忽略
            </Button>
            <Button size="small" danger type="primary" onClick={() => router.push('/tickets')} className="text-xs">
              查看最新工单
            </Button>
          </div>
        </div>
      )}

      {(loading || loadError) ? (
        <LoadingEmptyError
          state={loading ? 'loading' : 'error'}
          loadingText="正在加载运营数据..."
          error={{ description: '运营看板数据加载失败，请稍后重试', onAction: loadBoard, actionText: '重试' }}
        />
      ) : (
        <>
          {/* 2. 核心 KPI 数据卡片 */}
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            <div className="p-5 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-sm">
              <div className="text-xs font-semibold text-slate-400">今日新增工单量</div>
              <div className="text-2xl font-black text-slate-900 dark:text-slate-100 mt-1">
                {todayNew ?? '暂无数据'}
              </div>
              <div className="text-xs text-slate-400 mt-2 flex items-center gap-1">
                <ArrowUpRight size={14} /> 截至当前时刻
              </div>
            </div>

            <div className="p-5 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-sm">
              <div className="text-xs font-semibold text-slate-400">当前 SLA 达成率</div>
              <div className="text-2xl font-black text-emerald-600 mt-1">
                {slaCompliance != null ? `${slaCompliance.toFixed(1)}%` : '暂无数据'}
              </div>
              <div className="text-xs text-slate-400 mt-2">基于实时 SLA 监控</div>
            </div>

            <div className="p-5 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-sm">
              <div className="text-xs font-semibold text-slate-400">平均首次响应时长</div>
              <div className="text-2xl font-black text-primary-600 mt-1">{formatMinutes(avgResponseTime ?? undefined)}</div>
              <div className="text-xs text-slate-400 mt-2">基于实时 SLA 监控</div>
            </div>

            <div className="p-5 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-sm">
              <div className="text-xs font-semibold text-slate-400">待分配工单池</div>
              <div className="text-2xl font-black text-amber-500 mt-1">
                {unassignedCount != null ? `${unassignedCount} 张` : '暂无数据'}
              </div>
              <div className="text-xs text-amber-600 font-medium mt-2">
                {unassignedCount && unassignedCount > 0 ? '建议触发负载均衡派发' : '当前无积压'}
              </div>
            </div>
          </div>

          {/* 3. 团队成员工作负载均衡监控（真实在办工单数） */}
          <div className="bg-white dark:bg-slate-900 rounded-2xl p-6 border border-slate-200 dark:border-slate-800 shadow-sm">
            <div className="flex items-center justify-between mb-4">
              <div>
                <h3 className="text-base font-bold text-slate-900 dark:text-slate-100 m-0">
                  团队工作负载看板 (Team Workload)
                </h3>
                <p className="text-xs text-slate-400 mt-0.5">按当前"处理中"工单数实时聚合，仅展示负载最高的 6 人</p>
              </div>
              <Tag color="blue">
                <Users size={12} className="inline mr-1" />
                在办人员: {teamWorkload.length} 人
              </Tag>
            </div>

            {teamWorkload.length === 0 ? (
              <div className="text-xs text-slate-400 py-6 text-center">当前没有处理中的工单</div>
            ) : (
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
                {teamWorkload.map((mem, idx) => {
                  const isLightest = idx === teamWorkload.length - 1 && teamWorkload.length > 1;
                  const percent = Math.min(Math.round((mem.value / WORKLOAD_DISPLAY_CAP) * 100), 100);
                  return (
                    <div
                      key={mem.name}
                      className={`p-4 rounded-xl border transition-all ${
                        isLightest
                          ? 'bg-emerald-50/40 dark:bg-emerald-950/20 border-emerald-300 dark:border-emerald-800 shadow-sm'
                          : 'bg-slate-50 dark:bg-slate-800/40 border-slate-200/80 dark:border-slate-800'
                      }`}
                    >
                      <div className="flex items-center justify-between mb-2">
                        <div className="flex items-center gap-2">
                          <Avatar size={32} className={isLightest ? 'bg-emerald-600' : 'bg-slate-600'}>
                            {mem.name.charAt(0)}
                          </Avatar>
                          <div className="text-xs font-bold text-slate-800 dark:text-slate-100">{mem.name}</div>
                        </div>
                        {isLightest && (
                          <span className="text-[10px] bg-emerald-100 dark:bg-emerald-900/60 text-emerald-700 dark:text-emerald-300 font-bold px-1.5 py-0.5 rounded">
                            负载最低
                          </span>
                        )}
                      </div>

                      <div className="space-y-1">
                        <div className="flex items-center justify-between text-xs text-slate-500">
                          <span>处理中工单：{mem.value}</span>
                          <span className="font-semibold">{percent}%</span>
                        </div>
                        <Progress
                          percent={percent}
                          size="small"
                          status={mem.value >= WORKLOAD_DISPLAY_CAP - 1 ? 'exception' : 'normal'}
                          strokeColor={isLightest ? '#10b981' : undefined}
                        />
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}

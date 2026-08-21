'use client';

import React from 'react';
import { Users, Workflow, BookOpen, AlertCircle, TrendingUp, BarChart3 } from 'lucide-react';
import type { AdminStats } from '../hooks/useAdminData';

interface SystemOverviewProps {
  stats?: AdminStats;
  loading?: boolean;
}

const GRADIENTS = {
  accent: 'from-orange-500 to-orange-700',
  success: 'from-emerald-500 to-emerald-600',
  purple: 'from-purple-500 to-purple-700',
  warning: 'from-amber-500 to-amber-600',
} as const;

const TREND_CLASSES = {
  up: 'bg-emerald-50 text-emerald-600 dark:bg-emerald-950/40 dark:text-emerald-400',
  down: 'bg-red-50 text-red-600 dark:bg-red-950/40 dark:text-red-400',
} as const;

export const SystemOverview: React.FC<SystemOverviewProps> = ({ stats }) => {
  const formatValue = (value: string | number | null | undefined) => {
    if (value === null || value === undefined || value === '') {
      return '—';
    }
    return value;
  };

  const systemStats = [
    {
      title: '活跃用户',
      value: formatValue(stats?.activeUsers),
      change: '+12%',
      icon: Users,
      gradient: GRADIENTS.accent,
      trend: 'up' as const,
      description: stats?.activeUsers == null ? '暂无真实数据接入' : '较上月新增132位活跃用户',
      progress: 78,
      placeholder: stats?.activeUsers == null,
    },
    {
      title: '运行中的流程',
      value: formatValue(stats?.runningWorkflows),
      change: '+6.7%',
      icon: Workflow,
      gradient: GRADIENTS.success,
      trend: 'up' as const,
      description: stats?.runningWorkflows == null ? '暂无真实数据接入' : '3个工作流新启动',
      progress: 65,
      placeholder: stats?.runningWorkflows == null,
    },
    {
      title: '服务目录项',
      value: formatValue(stats?.serviceCatalogItems),
      change: '+5.9%',
      icon: BookOpen,
      gradient: GRADIENTS.purple,
      trend: 'up' as const,
      description: stats?.serviceCatalogItems == null ? '服务目录数据待接入' : '新增5项服务目录',
      progress: 82,
      placeholder: stats?.serviceCatalogItems == null,
    },
    {
      title: '系统告警',
      value: formatValue(stats?.systemAlerts),
      change: '-33%',
      icon: AlertCircle,
      gradient: GRADIENTS.warning,
      trend: 'down' as const,
      description: stats?.systemAlerts == null ? '告警数据待接入' : '较昨日减少1个告警',
      progress: 15,
      placeholder: stats?.systemAlerts == null,
    },
  ];

  return (
    <div>
      {/* 页面标题区 */}
      <div className="mb-6 pb-4 border-b border-slate-200 dark:border-slate-800">
        <div className="flex items-center gap-3 mb-2">
          <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-orange-500 to-orange-700 flex items-center justify-center text-white shadow-sm">
            <BarChart3 size={20} />
          </div>
          <h3 className="text-xl font-bold text-slate-900 dark:text-slate-100 m-0 tracking-tight">系统概览</h3>
        </div>
        <p className="text-sm text-slate-500 dark:text-slate-400 m-0">实时监控系统关键指标和业务健康状态</p>
      </div>

      {/* 统计卡片网格 */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-5">
        {systemStats.map((stat) => {
          const Icon = stat.icon;
          return (
            <div
              key={stat.title}
              className="relative overflow-hidden p-6 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-sm hover:shadow-md transition-shadow animate-in fade-in duration-300"
            >
              <div className="flex items-start justify-between mb-5">
                <div
                  className={`w-[52px] h-[52px] rounded-xl bg-gradient-to-br ${stat.gradient} flex items-center justify-center text-white shadow-sm`}
                >
                  <Icon size={24} />
                </div>
                <div
                  className={`flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-semibold ${TREND_CLASSES[stat.trend]}`}
                >
                  <TrendingUp size={14} className={stat.trend === 'down' ? 'rotate-180' : ''} />
                  <span>{stat.change}</span>
                </div>
              </div>

              <div className="text-sm font-medium text-slate-500 dark:text-slate-400 mb-1.5">{stat.title}</div>

              <div
                className={`text-3xl font-bold tracking-tight mb-3 ${
                  stat.placeholder ? 'text-slate-400 dark:text-slate-500' : 'text-slate-900 dark:text-slate-100'
                }`}
              >
                {stat.value}
              </div>

              <div className="flex items-center justify-between mb-1.5">
                <span className="text-xs text-slate-500 dark:text-slate-400">{stat.description}</span>
                {!stat.placeholder && (
                  <span className="text-xs font-semibold text-slate-600 dark:text-slate-300">{stat.progress}%</span>
                )}
              </div>
              <div className={`h-1.5 rounded-full bg-slate-100 dark:bg-slate-800 overflow-hidden ${stat.placeholder ? 'opacity-50' : ''}`}>
                <div
                  className={`h-full rounded-full bg-gradient-to-r ${stat.gradient} transition-all duration-1000`}
                  style={{ width: `${stat.placeholder ? 6 : stat.progress}%` }}
                />
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
};

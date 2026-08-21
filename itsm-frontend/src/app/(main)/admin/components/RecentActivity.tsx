'use client';

import React from 'react';
import { Activity, Users, Workflow, Shield, BookOpen, Bell, Clock } from 'lucide-react';
import { useI18n } from '@/lib/i18n';

const recentActivities = [
  {
    id: 1,
    type: 'user_created',
    title: '新用户注册',
    description: '张三 加入了系统',
    time: '2分钟前',
    icon: Users,
    color: 'bg-blue-100 text-blue-600 dark:bg-blue-950/50 dark:text-blue-400',
  },
  {
    id: 2,
    type: 'workflow_updated',
    title: '工作流更新',
    description: '事件管理流程已更新',
    time: '1小时前',
    icon: Workflow,
    color: 'bg-emerald-100 text-emerald-600 dark:bg-emerald-950/50 dark:text-emerald-400',
  },
  {
    id: 3,
    type: 'role_assigned',
    title: '角色分配',
    description: '为李四分配了管理员角色',
    time: '3小时前',
    icon: Shield,
    color: 'bg-purple-100 text-purple-600 dark:bg-purple-950/50 dark:text-purple-400',
  },
  {
    id: 4,
    type: 'service_added',
    title: '服务目录更新',
    description: '新增云存储服务项',
    time: '5小时前',
    icon: BookOpen,
    color: 'bg-orange-100 text-orange-600 dark:bg-orange-950/50 dark:text-orange-400',
  },
  {
    id: 5,
    type: 'notification_sent',
    title: '通知发送',
    description: '系统维护通知已发送',
    time: '1天前',
    icon: Bell,
    color: 'bg-amber-100 text-amber-600 dark:bg-amber-950/50 dark:text-amber-400',
  },
];

export const RecentActivity: React.FC = () => {
  const { t } = useI18n();

  return (
    <div className="h-full bg-white dark:bg-slate-900 rounded-2xl border border-slate-200 dark:border-slate-800 shadow-sm p-5">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2 text-sm font-bold text-slate-900 dark:text-slate-100">
          <Activity size={18} />
          <span>{t('admin.recentActivity')}</span>
          <span className="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-amber-100 text-amber-700 dark:bg-amber-950/50 dark:text-amber-300">
            示例数据
          </span>
        </div>
        <button className="text-xs font-semibold text-primary-600 hover:text-primary-500">
          {t('admin.viewAll')}
        </button>
      </div>

      <div className="divide-y divide-slate-100 dark:divide-slate-800">
        {recentActivities.map((activity) => {
          const Icon = activity.icon;
          return (
            <div key={activity.id} className="py-3 flex items-start gap-3">
              <div className={`w-9 h-9 rounded-full flex items-center justify-center flex-shrink-0 ${activity.color}`}>
                <Icon size={16} />
              </div>
              <div className="flex-1 min-w-0">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs font-bold text-slate-800 dark:text-slate-200">{activity.title}</span>
                  <span className="flex items-center gap-1 text-[11px] text-slate-400 flex-shrink-0">
                    <Clock size={11} />
                    {activity.time}
                  </span>
                </div>
                <div className="text-xs text-slate-500 dark:text-slate-400 mt-0.5">{activity.description}</div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
};

'use client';

import React from 'react';
import Link from 'next/link';
import {
  Users,
  Workflow,
  Settings,
  Zap,
  BookOpen,
  ChevronRight,
  UserCheck,
  UserCog,
  UserPlus,
  Lock,
  CheckSquare,
  Clock,
  AlertTriangle,
  Megaphone,
  Folder,
  Cog,
} from 'lucide-react';
import { useI18n } from '@/lib/i18n';
import type { LucideIcon } from 'lucide-react';

interface QuickActionItem {
  title: string;
  desc: string;
  href: string;
  count: number;
  icon: LucideIcon;
  iconBg: string;
  iconText: string;
  badgeClass: string;
}

const actionGroups = [
  {
    id: 'users',
    title: '用户与权限',
    subtitle: '管理用户账户、角色和权限',
    icon: Users,
    accentClass: 'bg-gradient-to-br from-orange-500 to-orange-700 shadow-orange-500/30',
    items: [
      {
        title: '用户管理',
        desc: '用户账户与组织',
        href: '/admin/users',
        count: 1234,
        icon: UserCheck,
        iconBg: 'bg-orange-50 border-orange-200 dark:bg-orange-950/30 dark:border-orange-900',
        iconText: 'text-orange-600',
        badgeClass: 'bg-orange-100 text-orange-700 dark:bg-orange-950/50 dark:text-orange-300',
      },
      {
        title: '角色管理',
        desc: '角色与权限配置',
        href: '/admin/roles',
        count: 15,
        icon: UserCog,
        iconBg: 'bg-indigo-50 border-indigo-200 dark:bg-indigo-950/30 dark:border-indigo-900',
        iconText: 'text-indigo-600',
        badgeClass: 'bg-indigo-100 text-indigo-700 dark:bg-indigo-950/50 dark:text-indigo-300',
      },
      {
        title: '用户组',
        desc: '组织架构管理',
        href: '/admin/groups',
        count: 28,
        icon: UserPlus,
        iconBg: 'bg-cyan-50 border-cyan-200 dark:bg-cyan-950/30 dark:border-cyan-900',
        iconText: 'text-cyan-600',
        badgeClass: 'bg-cyan-100 text-cyan-700 dark:bg-cyan-950/50 dark:text-cyan-300',
      },
      {
        title: '权限矩阵',
        desc: '细粒度权限控制',
        href: '/admin/permissions',
        count: 156,
        icon: Lock,
        iconBg: 'bg-purple-50 border-purple-200 dark:bg-purple-950/30 dark:border-purple-900',
        iconText: 'text-purple-600',
        badgeClass: 'bg-purple-100 text-purple-700 dark:bg-purple-950/50 dark:text-purple-300',
      },
    ] satisfies QuickActionItem[],
  },
  {
    id: 'process',
    title: '流程与自动化',
    subtitle: '配置工作流和审批规则',
    icon: Workflow,
    accentClass: 'bg-gradient-to-br from-emerald-500 to-emerald-700 shadow-emerald-500/30',
    items: [
      {
        title: '工作流设计',
        desc: 'BPMN流程编排',
        href: '/admin/workflows',
        count: 45,
        icon: Workflow,
        iconBg: 'bg-emerald-50 border-emerald-200 dark:bg-emerald-950/30 dark:border-emerald-900',
        iconText: 'text-emerald-600',
        badgeClass: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-300',
      },
      {
        title: '审批链',
        desc: '多级审批规则',
        href: '/admin/approval-chains',
        count: 12,
        icon: CheckSquare,
        iconBg: 'bg-teal-50 border-teal-200 dark:bg-teal-950/30 dark:border-teal-900',
        iconText: 'text-teal-600',
        badgeClass: 'bg-teal-100 text-teal-700 dark:bg-teal-950/50 dark:text-teal-300',
      },
      {
        title: 'SLA定义',
        desc: '服务级别协议',
        href: '/admin/sla-definitions',
        count: 8,
        icon: Clock,
        iconBg: 'bg-amber-50 border-amber-200 dark:bg-amber-950/30 dark:border-amber-900',
        iconText: 'text-amber-600',
        badgeClass: 'bg-amber-100 text-amber-700 dark:bg-amber-950/50 dark:text-amber-300',
      },
      {
        title: '升级规则',
        desc: '自动升级策略',
        href: '/admin/escalation-rules',
        count: 6,
        icon: AlertTriangle,
        iconBg: 'bg-orange-50 border-orange-200 dark:bg-orange-950/30 dark:border-orange-900',
        iconText: 'text-orange-600',
        badgeClass: 'bg-orange-100 text-orange-700 dark:bg-orange-950/50 dark:text-orange-300',
      },
    ] satisfies QuickActionItem[],
  },
  {
    id: 'system',
    title: '系统配置',
    subtitle: '服务目录和通知设置',
    icon: Settings,
    accentClass: 'bg-gradient-to-br from-purple-500 to-purple-700 shadow-purple-500/30',
    items: [
      {
        title: '服务目录',
        desc: '服务项管理',
        href: '/admin/service-catalogs',
        count: 89,
        icon: BookOpen,
        iconBg: 'bg-pink-50 border-pink-200 dark:bg-pink-950/30 dark:border-pink-900',
        iconText: 'text-pink-600',
        badgeClass: 'bg-pink-100 text-pink-700 dark:bg-pink-950/50 dark:text-pink-300',
      },
      {
        title: '通知配置',
        desc: '消息推送规则',
        href: '/notifications',
        count: 24,
        icon: Megaphone,
        iconBg: 'bg-red-50 border-red-200 dark:bg-red-950/30 dark:border-red-900',
        iconText: 'text-red-600',
        badgeClass: 'bg-red-100 text-red-700 dark:bg-red-950/50 dark:text-red-300',
      },
      {
        title: '工单分类',
        desc: '分类与模板',
        href: '/admin/ticket-categories',
        count: 32,
        icon: Folder,
        iconBg: 'bg-slate-100 border-slate-200 dark:bg-slate-800 dark:border-slate-700',
        iconText: 'text-slate-600 dark:text-slate-300',
        badgeClass: 'bg-slate-200 text-slate-700 dark:bg-slate-700 dark:text-slate-200',
      },
      {
        title: '系统设置',
        desc: '全局参数配置',
        href: '/admin/system-config',
        count: 67,
        icon: Cog,
        iconBg: 'bg-slate-900/5 border-slate-300 dark:bg-slate-800 dark:border-slate-700',
        iconText: 'text-slate-900 dark:text-slate-200',
        badgeClass: 'bg-slate-200 text-slate-800 dark:bg-slate-700 dark:text-slate-200',
      },
    ] satisfies QuickActionItem[],
  },
];

const ActionCard: React.FC<{ item: QuickActionItem }> = ({ item }) => {
  const Icon = item.icon;
  return (
    <Link href={item.href} className="group block h-full no-underline">
      <div className="relative h-full p-5 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-sm transition-all duration-300 group-hover:-translate-y-0.5 group-hover:shadow-lg overflow-hidden">
        <div className="flex items-start justify-between mb-3.5">
          <div className={`w-[42px] h-[42px] rounded-xl border flex items-center justify-center ${item.iconBg}`}>
            <Icon size={20} className={item.iconText} />
          </div>
          <span className={`px-2 py-0.5 rounded-full text-[11px] font-semibold ${item.badgeClass}`}>
            {item.count}
          </span>
        </div>

        <div className="text-[15px] font-semibold text-slate-900 dark:text-slate-100 mb-1.5">{item.title}</div>
        <div className="text-[13px] text-slate-500 dark:text-slate-400 leading-relaxed">{item.desc}</div>

        <div className="absolute right-4 bottom-4 opacity-0 -translate-x-2 transition-all duration-300 group-hover:opacity-100 group-hover:translate-x-0 text-slate-400">
          <ChevronRight size={18} />
        </div>
      </div>
    </Link>
  );
};

const GroupSection: React.FC<{ group: (typeof actionGroups)[number] }> = ({ group }) => {
  const Icon = group.icon;

  return (
    <div className="mb-8 animate-in fade-in duration-300">
      <div className="flex items-center gap-3.5 mb-5 pb-4 border-b border-slate-200 dark:border-slate-800">
        <div className={`w-11 h-11 rounded-xl flex items-center justify-center text-white shadow-sm ${group.accentClass}`}>
          <Icon size={22} />
        </div>
        <div>
          <h4 className="text-lg font-bold text-slate-900 dark:text-slate-100 m-0 tracking-tight">{group.title}</h4>
          <p className="text-[13px] text-slate-500 dark:text-slate-400 m-0">{group.subtitle}</p>
        </div>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        {group.items.map((item) => (
          <ActionCard key={item.href} item={item} />
        ))}
      </div>
    </div>
  );
};

export const QuickActions: React.FC = () => {
  useI18n();

  return (
    <div>
      {/* 主标题 */}
      <div className="mb-7 pb-5 border-b border-slate-200 dark:border-slate-800">
        <div className="flex items-center gap-3 mb-2">
          <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-amber-500 to-amber-700 flex items-center justify-center text-white shadow-sm">
            <Zap size={20} />
          </div>
          <h3 className="text-xl font-bold text-slate-900 dark:text-slate-100 m-0 tracking-tight">快捷操作</h3>
        </div>
        <p className="text-sm text-slate-500 dark:text-slate-400 m-0">快速访问系统管理和配置功能</p>
      </div>

      {actionGroups.map((group) => (
        <GroupSection key={group.id} group={group} />
      ))}
    </div>
  );
};

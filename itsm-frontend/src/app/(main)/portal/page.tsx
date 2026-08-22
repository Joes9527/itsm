'use client';

import React from 'react';
import { Card, Button, Tag, Row, Col } from 'antd';
import {
  Sparkles,
  KeyRound,
  Laptop,
  Mail,
  ShieldAlert,
  HelpCircle,
  Clock,
  CheckCircle2,
  ArrowRight,
  PlusCircle,
} from 'lucide-react';
import { useRouter } from 'next/navigation';
import { useAuthStore } from '@/lib/store/auth-store';
import { HeroSearchBar } from '@/components/portal/HeroSearchBar';
import { ManagerPendingApprovals } from '@/components/portal/ManagerPendingApprovals';

const POPULAR_CATALOGS = [
  {
    id: 1,
    title: '申请 Microsoft 365 Copilot 许可证',
    desc: '开通 M365 AI 办公副驾驶，赋能企业邮件与文档分析',
    icon: Sparkles,
    color: 'text-purple-600 bg-purple-50 dark:bg-purple-950/40 border-purple-200 dark:border-purple-800',
    tag: '热门 AI 采购',
  },
  {
    id: 2,
    title: 'VPN / 远程办公访问权限申请',
    desc: '申请安全接入公司内部网络及各核心业务系统',
    icon: KeyRound,
    color: 'text-blue-600 bg-blue-50 dark:bg-blue-950/40 border-blue-200 dark:border-blue-800',
    tag: '基础权限',
  },
  {
    id: 3,
    title: '办公电脑 / 显示器硬件报修',
    desc: '电脑蓝屏、外设故障、网络接口损坏等物理硬件支持',
    icon: Laptop,
    color: 'text-emerald-600 bg-emerald-50 dark:bg-emerald-950/40 border-emerald-200 dark:border-emerald-800',
    tag: 'IT 桌面支持',
  },
  {
    id: 4,
    title: '企业邮箱 / 共享邮箱组创建',
    desc: '申请分公司共享业务公共邮箱或扩容个人邮箱容量',
    icon: Mail,
    color: 'text-amber-600 bg-amber-50 dark:bg-amber-950/40 border-amber-200 dark:border-amber-800',
    tag: '办公协同',
  },
];

const RECENT_REQUESTS = [
  {
    id: 'REQ-2026-0801',
    title: 'Microsoft 365 Copilot 许可证采购申请',
    status: '审批中',
    statusStep: '总经理审批中 (流程实例: copilot_procurement)',
    statusColor: 'processing',
    updatedAt: '今天 14:20',
  },
  {
    id: 'INC-2026-0792',
    title: '华南分公司 3 楼会议室 Wi-Fi 偶发性掉线',
    status: '已解决',
    statusStep: 'L2 网络组已完成 AP 固件升级与信道调优',
    statusColor: 'success',
    updatedAt: '昨天 17:45',
  },
];

export default function PortalPage() {
  const router = useRouter();
  const { user } = useAuthStore();

  const userName = user?.name || user?.username || '伙伴';

  return (
    <div className="space-y-8 animate-in fade-in duration-300">
      {/* 1. 欢迎横幅 & 智能自愈搜索框 */}
      <div className="text-center pt-4">
        <h1 className="text-3xl font-extrabold text-slate-900 dark:text-slate-50 tracking-tight">
          您好，{userName}！有什么我们可以帮您？
        </h1>
        <p className="text-sm text-slate-500 mt-1 max-w-xl mx-auto">
          快速搜索企业知识库自愈排障、提报 IT 服务申请或实时跟踪您的工单进展
        </p>
        <HeroSearchBar />
      </div>

      {/* 2. 经理/主管专属审批卡片 */}
      <ManagerPendingApprovals />

      {/* 3. 常用服务目录卡片 */}
      <div>
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="text-lg font-bold text-slate-900 dark:text-slate-100 m-0">常用服务目录申请</h3>
            <p className="text-xs text-slate-500 mt-0.5">选择您所需的服务模板，快速提交审批与流转</p>
          </div>
          <Button
            type="link"
            className="text-xs font-semibold flex items-center gap-1"
            onClick={() => router.push('/service-catalog')}
          >
            全部服务目录 <ArrowRight size={14} />
          </Button>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
          {POPULAR_CATALOGS.map((item) => {
            const Icon = item.icon;
            return (
              <div
                key={item.id}
                onClick={() => router.push(`/service-catalog`)}
                className="group relative p-5 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 hover:border-primary-500/80 shadow-sm hover:shadow-lg transition-all cursor-pointer flex flex-col justify-between"
              >
                <div>
                  <div className="flex items-center justify-between mb-3">
                    <div className={`p-2.5 rounded-xl border ${item.color}`}>
                      <Icon size={20} />
                    </div>
                    <span className="text-[11px] font-medium text-slate-400 bg-slate-50 dark:bg-slate-800 px-2 py-0.5 rounded-md">
                      {item.tag}
                    </span>
                  </div>
                  <h4 className="text-sm font-bold text-slate-900 dark:text-slate-100 group-hover:text-primary-600 transition-colors">
                    {item.title}
                  </h4>
                  <p className="text-xs text-slate-500 mt-1 line-clamp-2">
                    {item.desc}
                  </p>
                </div>

                <div className="mt-4 pt-3 border-t border-slate-100 dark:border-slate-800 flex items-center justify-between text-xs font-semibold text-primary-600">
                  <span>立即申请</span>
                  <ArrowRight size={14} className="group-hover:translate-x-1 transition-transform" />
                </div>
              </div>
            );
          })}
        </div>
      </div>

      {/* 4. 我的近期请求时间轴 */}
      <div className="bg-white dark:bg-slate-900 rounded-2xl p-6 border border-slate-200/80 dark:border-slate-800 shadow-sm">
        <div className="flex items-center justify-between mb-5">
          <div className="flex items-center gap-2">
            <Clock size={18} className="text-primary-600" />
            <h3 className="text-base font-bold text-slate-900 dark:text-slate-100 m-0">我的近期请求追踪</h3>
          </div>
          <Button
            size="small"
            onClick={() => router.push('/my-requests')}
            className="text-xs"
          >
            查看全部我的工单
          </Button>
        </div>

        <div className="space-y-4">
          {RECENT_REQUESTS.map((req) => (
            <div
              key={req.id}
              onClick={() => router.push('/my-requests')}
              className="p-4 rounded-xl bg-slate-50 dark:bg-slate-800/60 hover:bg-slate-100/80 border border-slate-100 dark:border-slate-800 flex flex-col md:flex-row md:items-center justify-between gap-3 cursor-pointer transition-all"
            >
              <div>
                <div className="flex items-center gap-2">
                  <span className="text-xs font-mono font-semibold text-slate-400">{req.id}</span>
                  <span className="text-sm font-bold text-slate-900 dark:text-slate-100">{req.title}</span>
                  <Tag color={req.statusColor} className="text-[11px] m-0">{req.status}</Tag>
                </div>
                <div className="text-xs text-slate-500 mt-1.5 flex items-center gap-2">
                  <span className="text-slate-700 dark:text-slate-300 font-medium">当前进度：{req.statusStep}</span>
                  <span>•</span>
                  <span>更新于 {req.updatedAt}</span>
                </div>
              </div>

              <div className="flex items-center gap-1 text-xs text-primary-600 font-semibold self-end md:self-center">
                <span>详情</span>
                <ArrowRight size={14} />
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

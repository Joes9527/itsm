'use client';

import React, { useEffect, useState } from 'react';
import { Button, Tag, Spin, Alert } from 'antd';
import { Sparkles, Clock, ArrowRight, Inbox } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { useAuthStore } from '@/lib/store/auth-store';
import { HeroSearchBar } from '@/components/portal/HeroSearchBar';
import { ManagerPendingApprovals } from '@/components/portal/ManagerPendingApprovals';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { ServiceStatus, type ServiceItem } from '@/types/service-catalog';

// 与 my-requests 页面 RequestStatusBadge 使用同一套 Ticket 状态词表（见 src/types/ticket.ts）
const TICKET_STATUS_CONFIG: Record<string, { label: string; color: string }> = {
  new: { label: '新建', color: 'gold' },
  open: { label: '待处理', color: 'gold' },
  in_progress: { label: '处理中', color: 'processing' },
  pending: { label: '待处理', color: 'blue' },
  resolved: { label: '已解决', color: 'success' },
  closed: { label: '已关闭', color: 'default' },
  cancelled: { label: '已取消', color: 'default' },
};

function formatUpdatedAt(dateString?: string): string {
  if (!dateString) return '-';
  const time = new Date(dateString);
  if (Number.isNaN(time.getTime())) return '-';
  return time.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
}

interface RecentRequestItem {
  id: number;
  ticketId: number;
  title: string;
  statusLabel: string;
  statusColor: string;
  updatedAt: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function readString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value : undefined;
}

function readNumber(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value === 'string' && value.trim()) {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : undefined;
  }
  return undefined;
}

function toRecentRequest(value: unknown): RecentRequestItem | null {
  if (!isRecord(value)) return null;

  const id = readNumber(value.id);
  const ticketId = readNumber(value.ticketId);
  if (id === undefined || ticketId === undefined) return null;

  const catalogName = isRecord(value.catalog) ? readString(value.catalog.name) : undefined;
  const ticketStatus = readString(value.ticketStatus);
  const statusConfig = ticketStatus
    ? TICKET_STATUS_CONFIG[ticketStatus] || { label: ticketStatus, color: 'default' }
    : { label: '-', color: 'default' };

  return {
    id,
    ticketId,
    title: readString(value.ticketTitle) || catalogName || '-',
    statusLabel: statusConfig.label,
    statusColor: statusConfig.color,
    updatedAt: formatUpdatedAt(readString(value.updatedAt) || readString(value.createdAt)),
  };
}

export default function PortalPage() {
  const router = useRouter();
  const { user } = useAuthStore();

  const userName = user?.name || user?.username || '伙伴';

  const [catalogs, setCatalogs] = useState<ServiceItem[]>([]);
  const [catalogsLoading, setCatalogsLoading] = useState(true);
  const [catalogsError, setCatalogsError] = useState<string | null>(null);

  const [recentRequests, setRecentRequests] = useState<RecentRequestItem[]>([]);
  const [requestsLoading, setRequestsLoading] = useState(true);
  const [requestsError, setRequestsError] = useState<string | null>(null);

  const loadCatalogs = async () => {
    setCatalogsLoading(true);
    setCatalogsError(null);
    try {
      const { services } = await ServiceCatalogApi.getServices({
        status: ServiceStatus.PUBLISHED,
        page: 1,
        pageSize: 4,
      });
      setCatalogs(services);
    } catch (e) {
      setCatalogsError('常用服务目录加载失败');
      setCatalogs([]);
    } finally {
      setCatalogsLoading(false);
    }
  };

  const loadRecentRequests = async () => {
    setRequestsLoading(true);
    setRequestsError(null);
    try {
      const { requests } = await ServiceCatalogApi.getServiceRequests({ page: 1, pageSize: 3 });
      setRecentRequests(requests.map(toRecentRequest).filter((request): request is RecentRequestItem => request !== null));
    } catch (e) {
      setRequestsError('近期请求加载失败');
      setRecentRequests([]);
    } finally {
      setRequestsLoading(false);
    }
  };

  useEffect(() => {
    loadCatalogs();
    loadRecentRequests();
  }, []);

  return (
    <div className="space-y-8 animate-in fade-in duration-300">
      {/* 1. 欢迎横幅与服务入口 */}
      <div className="text-center pt-4">
        <h1 className="text-[24px] font-semibold text-foreground tracking-tight">
          您好，{userName}！有什么我们可以帮您？
        </h1>
        <p className="text-sm text-muted mt-1 max-w-xl mx-auto">
          提交问题、申请 IT 服务，或跟踪您的工单处理进展
        </p>
        <HeroSearchBar />
      </div>

      {/* 2. 经理/主管专属审批卡片 */}
      <ManagerPendingApprovals />

      {/* 3. 常用服务目录卡片 */}
      <div>
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="text-[15px] font-semibold text-foreground m-0">常用服务目录申请</h3>
            <p className="text-[12px] text-muted mt-0.5">选择您所需的服务模板，快速提交审批与流转</p>
          </div>
          <Button
            type="link"
            className="text-[12px] font-semibold flex items-center gap-1"
            onClick={() => router.push('/service-catalog')}
          >
            全部服务目录 <ArrowRight size={14} />
          </Button>
        </div>

        {catalogsLoading ? (
          <div className="flex justify-center py-8">
            <Spin size="small" />
          </div>
        ) : catalogsError ? (
          <Alert
            type="error"
            showIcon
            title={catalogsError}
            action={
              <Button size="small" onClick={loadCatalogs}>
                重试
              </Button>
            }
          />
        ) : catalogs.length === 0 ? (
          <div className="text-center py-8 text-[13px] text-muted">暂无可申请的服务目录</div>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            {catalogs.map((item) => (
              <button
                type="button"
                key={item.id}
                onClick={() => router.push(`/service-catalog/request/${item.id}`)}
                className="group relative p-5 rounded-[8px] bg-surface border border-border hover:border-primary-500/80 shadow-none transition-all cursor-pointer flex flex-col justify-between text-left"
              >
                <div>
                  <div className="flex items-center justify-between mb-3">
                    <div className="p-2.5 rounded-[8px] border text-foreground bg-selected border-primary-200">
                      <Sparkles size={20} />
                    </div>
                    <span className="text-[11px] font-medium text-muted bg-raised px-2 py-0.5 rounded-md">
                      {item.category}
                    </span>
                  </div>
                  <h4 className="text-[15px] font-semibold text-foreground group-hover:text-foreground transition-colors">
                    {item.name}
                  </h4>
                  <p className="text-[12px] text-muted mt-1 line-clamp-2">
                    {item.shortDescription}
                  </p>
                </div>

                <div className="mt-4 pt-3 border-t border-border flex items-center justify-between text-[12px] font-semibold text-foreground">
                  <span>立即申请</span>
                  <ArrowRight size={14} className="group-hover:translate-x-1 transition-transform" />
                </div>
              </button>
            ))}
          </div>
        )}
      </div>

      {/* 4. 我的近期请求时间轴 */}
      <div className="bg-surface rounded-[8px] p-6 border border-border shadow-none">
        <div className="flex items-center justify-between mb-5">
          <div className="flex items-center gap-2">
            <Clock size={18} className="text-foreground" />
            <h3 className="text-[15px] font-semibold text-foreground m-0">我的近期请求追踪</h3>
          </div>
          <Button
            size="small"
            onClick={() => router.push('/my-requests')}
            className="text-[12px]"
          >
            查看全部我的工单
          </Button>
        </div>

        {requestsLoading ? (
          <div className="flex justify-center py-6">
            <Spin size="small" />
          </div>
        ) : requestsError ? (
          <Alert
            type="error"
            showIcon
            title={requestsError}
            action={
              <Button size="small" onClick={loadRecentRequests}>
                重试
              </Button>
            }
          />
        ) : recentRequests.length === 0 ? (
          <div className="text-center py-6 text-[13px] text-muted flex flex-col items-center gap-2">
            <Inbox size={24} className="text-muted" />
            暂无近期请求
          </div>
        ) : (
          <div className="space-y-4">
            {recentRequests.map((req) => (
              <button
                type="button"
                key={req.id}
                onClick={() => router.push(`/tickets/${req.ticketId}`)}
                className="w-full p-4 rounded-[8px] bg-raised hover:bg-raised border border-border flex flex-col md:flex-row md:items-center justify-between gap-3 cursor-pointer transition-all text-left"
              >
                <div>
                  <div className="flex items-center gap-2">
                    <span className="text-[13px] font-semibold text-foreground">{req.title}</span>
                    <Tag color={req.statusColor} className="text-[11px] m-0">{req.statusLabel}</Tag>
                  </div>
                  <div className="text-[12px] text-muted mt-1.5">
                    更新于 {req.updatedAt}
                  </div>
                </div>

                <div className="flex items-center gap-1 text-[12px] text-foreground font-semibold self-end md:self-center">
                  <span>详情</span>
                  <ArrowRight size={14} />
                </div>
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

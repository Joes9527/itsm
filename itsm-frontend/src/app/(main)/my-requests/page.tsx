'use client';

import React, { useCallback, useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { Alert, Button, Card, Empty, Input, Pagination, Select, Spin, Tag } from 'antd';
import {
  Calendar,
  CheckCircle,
  Clock,
  Filter,
  Hourglass,
  Search,
  User,
  UserCheck,
  XCircle,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import {
  FileTextOutlined,
  RightOutlined,
  SyncOutlined,
} from '@ant-design/icons';

import { ApiError } from '@/lib/api/http-client';
import type { Ticket } from '@/lib/api/api-config';
import {
  ticketService,
  TicketStatus,
  type ListTicketsParams,
} from '@/lib/services/ticket-service';
import { useAuthStore } from '@/lib/store/auth-store';
import { useDebounce } from '@/lib/component-utils';

// 本页是"我的工单"：数据来自 GET /api/v1/tickets，覆盖 incident/problem/
// change_request/service_request_item/generic 全部 recordClass。行级可见范围由后端
// authorization.WorkItemReadScope 收窄（特权角色=整租户，其他人=申请人/处理人），
// 前端只叠加"我提交的 / 我处理的 / 全部"这一个范围条件，不自行推断授权。
// 状态与关键字同样交给服务端过滤，避免只筛当前页造成"查不到"的假象。

const PAGE_SIZE = 10;

type ScopeValue = 'mine' | 'handling' | 'all';

const SCOPE_OPTIONS: Array<{ value: ScopeValue; label: string }> = [
  { value: 'mine', label: '我提交的' },
  { value: 'handling', label: '我处理的' },
  { value: 'all', label: '全部' },
];

// recordClass 标签：只按 AGENTS.md 的 WorkItem 词汇表映射，未知值原样展示，
// 不做猜测性归类（也不把退休词汇映射回新词汇）。
const RECORD_CLASS_CONFIG: Record<string, { label: string; color: string }> = {
  incident: { label: '事件', color: 'red' },
  problem: { label: '问题', color: 'orange' },
  change_request: { label: '变更', color: 'purple' },
  service_request_item: { label: '服务请求', color: 'blue' },
  catalog_task: { label: '服务请求任务', color: 'cyan' },
  generic: { label: '工单', color: 'default' },
};

// 与后端 common/constants.go 的状态词汇保持一致
const STATUS_CONFIG: Record<string, { label: string; color: string; icon: LucideIcon }> = {
  new: { label: '新建', color: 'gold', icon: Clock },
  open: { label: '待处理', color: 'gold', icon: Clock },
  assigned: { label: '已派单', color: 'blue', icon: UserCheck },
  in_progress: { label: '处理中', color: 'processing', icon: Hourglass },
  pending: { label: '等待中', color: 'blue', icon: Hourglass },
  resolved: { label: '已解决', color: 'success', icon: CheckCircle },
  closed: { label: '已关闭', color: 'default', icon: CheckCircle },
  cancelled: { label: '已取消', color: 'default', icon: XCircle },
};

const STATUS_FILTER_OPTIONS: Array<{ value: TicketStatus; label: string }> = [
  { value: TicketStatus.NEW, label: '新建' },
  { value: TicketStatus.OPEN, label: '待处理' },
  { value: TicketStatus.ASSIGNED, label: '已派单' },
  { value: TicketStatus.IN_PROGRESS, label: '处理中' },
  { value: TicketStatus.PENDING, label: '等待中' },
  { value: TicketStatus.RESOLVED, label: '已解决' },
  { value: TicketStatus.CLOSED, label: '已关闭' },
  { value: TicketStatus.CANCELLED, label: '已取消' },
];

const PRIORITY_CONFIG: Record<string, { label: string; color: string }> = {
  low: { label: '低', color: 'green' },
  medium: { label: '中', color: 'orange' },
  high: { label: '高', color: 'red' },
  urgent: { label: '紧急', color: 'purple' },
  critical: { label: '紧急', color: 'purple' },
};

const formatDate = (dateString?: string) => {
  if (!dateString) return '-';
  const time = new Date(dateString);
  if (Number.isNaN(time.getTime())) return '-';
  return time.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
};

const StatusBadge = ({ status }: { status?: string }) => {
  const config = (status ? STATUS_CONFIG[status] : undefined) || {
    label: status || '-',
    color: 'default',
    icon: Clock,
  };
  const Icon = config.icon;

  return (
    <Tag color={config.color} className="flex items-center gap-1 px-2 py-1">
      <Icon className="w-3 h-3" aria-hidden="true" />
      {config.label}
    </Tag>
  );
};

const WorkItemCard = ({ item }: { item: Ticket }) => {
  const recordClass = (item.recordClass ? RECORD_CLASS_CONFIG[item.recordClass] : undefined) || {
    label: item.recordClass || '-',
    color: 'default',
  };
  const priority = (item.priority ? PRIORITY_CONFIG[item.priority] : undefined) || {
    label: item.priority || '-',
    color: 'default',
  };

  return (
    <Card className="mb-4 rounded-[8px] shadow-none border border-border transition-shadow">
      <div className="flex items-start justify-between mb-4">
        <div className="flex-1">
          <div className="flex items-center gap-3 mb-2 flex-wrap">
            <span className="text-[13px] font-mono text-muted bg-raised px-2 py-1 rounded">
              {item.ticketNumber || `#${item.id}`}
            </span>
            <Tag color={recordClass.color}>{recordClass.label}</Tag>
            <StatusBadge status={item.status} />
            <Tag color={priority.color}>{priority.label}</Tag>
          </div>
          <h3 className="text-[15px] font-semibold text-foreground mb-2">{item.title || '-'}</h3>
        </div>
      </div>

      <div className="flex items-center justify-between text-[13px] text-muted">
        <div className="flex items-center gap-4 flex-wrap">
          <div className="flex items-center gap-1">
            <User className="w-4 h-4" aria-hidden="true" />
            <span>
              申请人：
              {item.requester?.name || (item.requesterId ? `用户 #${item.requesterId}` : '-')}
            </span>
          </div>
          <div className="flex items-center gap-1">
            <UserCheck className="w-4 h-4" aria-hidden="true" />
            <span>
              处理人：
              {item.assignee?.name || (item.assigneeId ? `用户 #${item.assigneeId}` : '未分配')}
            </span>
          </div>
          <div className="flex items-center gap-1">
            <Calendar className="w-4 h-4" aria-hidden="true" />
            <span>{formatDate(item.createdAt)}</span>
          </div>
        </div>
        <Button
          type="link"
          href={`/tickets/${item.id}`}
          className="flex items-center gap-1 p-0 h-auto"
          icon={<RightOutlined aria-hidden="true" />}
          iconPlacement="end"
        >
          查看详情
        </Button>
      </div>
    </Card>
  );
};

const MyRequestsPage = () => {
  const { user, isAuthenticated } = useAuthStore();

  // 身份必须是可信的登录态：没有用户 id 时不发请求，避免匿名/半登录状态下
  // 还去请求一个"我的"范围（特权角色在该接口上是整租户范围）。
  const actorId = typeof user?.id === 'number' ? user.id : 0;
  const hasActor = isAuthenticated === true && Number.isSafeInteger(actorId) && actorId > 0;

  const [scope, setScope] = useState<ScopeValue>('mine');
  const [status, setStatus] = useState<TicketStatus | undefined>(undefined);
  const [searchTerm, setSearchTerm] = useState('');
  const [currentPage, setCurrentPage] = useState(1);
  const [workItems, setWorkItems] = useState<Ticket[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [permissionDenied, setPermissionDenied] = useState(false);

  const debouncedSearch = useDebounce(searchTerm, 300);

  // 切范围、改状态、输入关键字（防抖期间会先因翻页复位发一次）都可能并发多条请求，
  // 先发的可能后到。只让最后一次请求写状态，否则旧响应会覆盖当前筛选条件的结果，
  // 让列表与筛选框、分页总数对不上。
  const requestSeq = useRef(0);

  const fetchWorkItems = useCallback(async () => {
    const seq = ++requestSeq.current;
    const isLatest = () => seq === requestSeq.current;

    if (!hasActor) {
      setWorkItems([]);
      setTotal(0);
      setError(null);
      setPermissionDenied(false);
      setLoading(false);
      return;
    }

    setLoading(true);
    setError(null);
    setPermissionDenied(false);
    try {
      const params: ListTicketsParams = { page: currentPage, pageSize: PAGE_SIZE };
      if (status) params.status = status;
      const keyword = debouncedSearch.trim();
      if (keyword) params.keyword = keyword;
      if (scope === 'mine') params.requesterId = actorId;
      if (scope === 'handling') params.assigneeId = actorId;

      const data = await ticketService.listTickets(params);
      if (!isLatest()) return;
      setWorkItems(data.tickets || []);
      setTotal(data.total || 0);
    } catch (err) {
      if (!isLatest()) return;
      if (err instanceof ApiError && err.status === 403) {
        setPermissionDenied(true);
      } else {
        setError('加载工单失败，请重试');
      }
      setWorkItems([]);
      setTotal(0);
    } finally {
      if (isLatest()) setLoading(false);
    }
  }, [actorId, currentPage, debouncedSearch, hasActor, scope, status]);

  useEffect(() => {
    fetchWorkItems();
  }, [fetchWorkItems]);

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  const resetToFirstPage = () => setCurrentPage(1);

  return (
    <div className="min-h-screen p-6 bg-page">
      <div className="max-w-7xl mx-auto">
        {/* 页面头部 */}
        <div className="mb-8">
          <div className="flex items-center justify-between">
            <div>
              <h1 className="text-[24px] font-semibold text-foreground mb-1">我的工单</h1>
              <p className="text-muted">查看我提交、我处理的工单及其处理进展</p>
            </div>
            <Button
              onClick={fetchWorkItems}
              loading={loading}
              icon={<SyncOutlined aria-hidden="true" />}
            >
              刷新
            </Button>
          </div>
        </div>

        {/* 权限不足 */}
        {permissionDenied && (
          <Alert
            title="没有查看工单的权限"
            description="当前账号缺少查看工单的权限，请联系管理员开通。"
            type="warning"
            showIcon
            className="mb-6"
          />
        )}

        {/* 错误提示 */}
        {error && (
          <Alert
            title={error}
            description="请检查网络连接或稍后重试"
            type="error"
            showIcon
            className="mb-6"
            action={
              <Button size="small" onClick={fetchWorkItems}>
                重试
              </Button>
            }
          />
        )}

        {/* 范围、搜索与状态筛选 */}
        <Card className="mb-6 rounded-[8px] shadow-none border border-border">
          <div className="flex flex-col lg:flex-row gap-4">
            <div className="flex gap-2 flex-wrap">
              {SCOPE_OPTIONS.map(option => (
                <Button
                  key={option.value}
                  type={scope === option.value ? 'primary' : 'default'}
                  onClick={() => {
                    setScope(option.value);
                    resetToFirstPage();
                  }}
                  className={scope !== option.value ? 'bg-raised border-border' : ''}
                >
                  {option.label}
                </Button>
              ))}
            </div>

            <div className="flex-1">
              <Input
                placeholder="搜索工单标题或描述..."
                prefix={<Search className="text-muted w-4 h-4" aria-hidden="true" />}
                value={searchTerm}
                onChange={e => {
                  setSearchTerm(e.target.value);
                  resetToFirstPage();
                }}
                allowClear
                className="w-full"
              />
            </div>

            <div className="flex items-center gap-2">
              <Filter className="w-5 h-5 text-muted" aria-hidden="true" />
              <Select<TicketStatus | undefined>
                allowClear
                placeholder="全部状态"
                value={status}
                onChange={value => {
                  setStatus(value);
                  resetToFirstPage();
                }}
                options={STATUS_FILTER_OPTIONS}
                style={{ minWidth: 140 }}
              />
            </div>
          </div>
        </Card>

        {/* 工单列表 */}
        {loading ? (
          <div className="flex items-center justify-center py-12">
            <Spin size="large" />
          </div>
        ) : !permissionDenied && !error && workItems.length > 0 ? (
          <div>
            {workItems.map(item => (
              <WorkItemCard key={item.id} item={item} />
            ))}
          </div>
        ) : (
          !permissionDenied &&
          !error && (
            <Card className="text-center py-12 rounded-[8px] shadow-none border border-border">
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={
                  <div className="mb-4">
                    <h3 className="text-[15px] font-medium text-foreground mb-2">暂无工单</h3>
                    <p className="text-muted">
                      {hasActor ? '当前筛选条件下没有工单' : '请先登录后查看您的工单'}
                    </p>
                  </div>
                }
              >
                <Link href="/service-catalog">
                  <Button type="primary" icon={<FileTextOutlined aria-hidden="true" />}>
                    浏览服务目录
                  </Button>
                </Link>
              </Empty>
            </Card>
          )
        )}

        {/* 分页 */}
        {!permissionDenied && totalPages > 1 && (
          <Card
            className="mt-8 rounded-[8px] shadow-none border border-border"
            styles={{ body: { padding: '16px 24px' } }}
          >
            <div className="flex items-center justify-between">
              <div className="text-[13px] text-muted">
                显示第 {(currentPage - 1) * PAGE_SIZE + 1} - {Math.min(currentPage * PAGE_SIZE, total)}{' '}
                条，共 {total} 条记录
              </div>
              <Pagination
                current={currentPage}
                total={total}
                pageSize={PAGE_SIZE}
                onChange={page => setCurrentPage(page)}
                showSizeChanger={false}
              />
            </div>
          </Card>
        )}
      </div>
    </div>
  );
};

export default MyRequestsPage;

'use client';

import React, { useState, useEffect } from 'react';
import Link from 'next/link';
import { Card, Input, Button, Tag, Pagination, Spin, Empty, Select, Alert, message } from 'antd';
import {
  FileText,
  RefreshCw,
  ChevronRight,
  Clock,
  Hourglass,
  CheckCircle,
  XCircle,
  Calendar,
  Search,
  Filter,
} from 'lucide-react';

// API 接口类型定义
//
// 状态/标题已经全部委托给关联的 Ticket（Task 1 从 ServiceRequest 表移除了 status/title/reason）。
// ticketId 是跳转到工单详情页（/tickets/:ticketId，承载状态/审批/工作流）的依据；
// ticketTitle/ticketStatus 是列表接口批量回填的展示字段，值域是 Ticket 的状态
// （new/open/in_progress/pending/resolved/closed/cancelled，见 src/types/ticket.ts），
// 不再是服务请求自己的审批阶段（submitted/manager_approved/...）。
interface ServiceRequest {
  id: number;
  ticketId: number;
  catalogId: number;
  requesterId: number;
  ticketTitle?: string;
  ticketStatus?: string;
  createdAt: string;
  catalog?: {
    id: number;
    name: string;
    category: string;
    description: string;
  };
  requester?: {
    id: number;
    name: string;
    email: string;
  };
}

import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';

// 与 src/types/ticket.ts 的 TicketStatus 保持一致
const RequestStatusBadge = ({ status }: { status?: string }) => {
  const statusConfig = {
    new: { label: '新建', color: 'gold', icon: Clock },
    open: { label: '待处理', color: 'gold', icon: Clock },
    in_progress: { label: '处理中', color: 'processing', icon: Hourglass },
    pending: { label: '待处理', color: 'blue', icon: Hourglass },
    resolved: { label: '已解决', color: 'success', icon: CheckCircle },
    closed: { label: '已关闭', color: 'default', icon: CheckCircle },
    cancelled: { label: '已取消', color: 'default', icon: XCircle },
  };

  if (!status) {
    return <Tag color="default">-</Tag>;
  }
  const config = statusConfig[status as keyof typeof statusConfig] || {
    label: status,
    color: 'default',
    icon: Clock,
  };
  const Icon = config.icon;

  return (
    <Tag color={config.color} className="flex items-center gap-1 px-2 py-1">
      <Icon className="w-3 h-3" />
      {config.label}
    </Tag>
  );
};

const RequestCard = ({ request }: { request: ServiceRequest }) => {
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

  return (
    <Card
      className="mb-4 rounded-lg shadow-sm border border-gray-200 hover:shadow-md transition-shadow"
     
    >
      <div className="flex items-start justify-between mb-4">
        <div className="flex-1">
          <div className="flex items-center gap-3 mb-2">
            <span className="text-sm font-mono text-gray-500 bg-gray-50 px-2 py-1 rounded">
              REQ-{String(request.id).padStart(5, '0')}
            </span>
            <RequestStatusBadge status={request.ticketStatus} />
          </div>
          <h3 className="text-lg font-semibold text-gray-900 mb-2">
            {request.ticketTitle || request.catalog?.name || '未知服务'}
          </h3>
          <p className="text-sm text-gray-600 mb-3">{request.catalog?.description || '-'}</p>
        </div>
      </div>

      <div className="flex items-center justify-between text-sm text-gray-500">
        <div className="flex items-center gap-4">
          <div className="flex items-center gap-1">
            <Calendar className="w-4 h-4" />
            <span>{formatDate(request.createdAt)}</span>
          </div>
          <div className="flex items-center gap-1">
            <FileText className="w-4 h-4" />
            <span>{request.catalog?.category || '其他'}</span>
          </div>
        </div>
        <Link href={`/tickets/${request.ticketId}`}>
          <Button type="link" className="flex items-center gap-1 p-0 h-auto">
            查看详情
            <ChevronRight className="w-4 h-4" />
          </Button>
        </Link>
      </div>
    </Card>
  );
};

const MyRequestsPage = () => {
  const [requests, setRequests] = useState<ServiceRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState('all');
  const [searchTerm, setSearchTerm] = useState('');
  const [currentPage, setCurrentPage] = useState(1);
  const [totalPages, setTotalPages] = useState(1);
  const [total, setTotal] = useState(0);
  const pageSize = 10;

  // 获取服务请求数据
  const fetchRequests = async (page = 1, status = filter) => {
    setLoading(true);
    setError(null);
    try {
      const data = await ServiceCatalogApi.getServiceRequests({
        page,
        pageSize,
        status: (status === 'all' ? undefined : status) as any,
      });

      setRequests((data.requests || []) as ServiceRequest[]);
      setTotal(data.total || 0);
      setTotalPages(Math.max(1, Math.ceil((data.total || 0) / pageSize)));
    } catch (error) {
      console.error('API调用失败:', error);
      setError('加载失败，请重试');
      message.error('加载服务请求失败');
      setRequests([]);
      setTotal(0);
      setTotalPages(1);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchRequests(currentPage, filter);
  }, [currentPage, filter]);

  // 筛选数据
  const filteredRequests = requests.filter(request => {
    const matchesSearch =
      !searchTerm ||
      request.catalog?.name.toLowerCase().includes(searchTerm.toLowerCase()) ||
      (request.ticketTitle || '').toLowerCase().includes(searchTerm.toLowerCase());
    return matchesSearch;
  });

  // 动态计算过滤选项数量——按关联 ticket 状态统计（状态已经委托给 Ticket，
  // 值域是 new/open/in_progress/pending/resolved/closed/cancelled）
  const filterOptions = [
    { value: 'all', label: '全部', count: total },
    { value: 'open', label: '待处理', count: requests.filter(r => r.ticketStatus === 'open' || r.ticketStatus === 'new').length },
    { value: 'in_progress', label: '处理中', count: requests.filter(r => r.ticketStatus === 'in_progress').length },
    { value: 'resolved', label: '已解决', count: requests.filter(r => r.ticketStatus === 'resolved').length },
    { value: 'closed', label: '已关闭', count: requests.filter(r => r.ticketStatus === 'closed').length },
  ];

  return (
    <div className="min-h-screen p-6 bg-gray-50">
      <div className="max-w-7xl mx-auto">
        {/* 页面头部 */}
        <div className="mb-8">
          <div className="flex items-center justify-between">
            <div>
              <h1 className="text-2xl font-bold text-gray-900 mb-1">我的请求</h1>
              <p className="text-gray-500">查看和跟踪您提交的所有服务请求</p>
            </div>
            <Button
              onClick={() => fetchRequests(currentPage, filter)}
              icon={<RefreshCw className="w-4 h-4" />}
            >
              刷新
            </Button>
          </div>
        </div>

        {/* 错误提示 */}
        {error && (
          <Alert
            message={error}
            description="请检查网络连接或稍后重试"
            type="error"
            showIcon
            className="mb-6"
            action={
              <Button size="small" onClick={() => fetchRequests(currentPage, filter)}>
                重试
              </Button>
            }
          />
        )}

        {/* 搜索和筛选 */}
        <Card className="mb-6 rounded-lg shadow-sm border border-gray-200">
          <div className="flex flex-col lg:flex-row gap-4">
            {/* 搜索框 */}
            <div className="flex-1">
              <Input
                placeholder="搜索服务名称或描述..."
                prefix={<Search className="text-gray-400 w-4 h-4" />}
                value={searchTerm}
                onChange={e => setSearchTerm(e.target.value)}
                className="w-full"
              />
            </div>

            {/* 状态筛选 */}
            <div className="flex items-center gap-2">
              <Filter className="w-5 h-5 text-gray-500" />
              <div className="flex gap-2 flex-wrap">
                {filterOptions.map(option => (
                  <Button
                    key={option.value}
                    type={filter === option.value ? 'primary' : 'default'}
                    onClick={() => {
                      setFilter(option.value);
                      setCurrentPage(1);
                    }}
                    className={filter !== option.value ? 'bg-gray-50 border-gray-200' : ''}
                  >
                    {option.label}
                    {option.count > 0 && (
                      <span className="ml-1.5 text-xs opacity-75">({option.count})</span>
                    )}
                  </Button>
                ))}
              </div>
            </div>
          </div>
        </Card>

        {/* 请求列表 */}
        {loading ? (
          <div className="flex items-center justify-center py-12">
            <Spin size="large" />
          </div>
        ) : filteredRequests.length > 0 ? (
          <div className="space-y-4">
            {filteredRequests.map(request => (
              <RequestCard key={request.id} request={request} />
            ))}
          </div>
        ) : (
          <Card
            className="text-center py-12 rounded-lg shadow-sm border border-gray-200"
           
          >
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={
                <div className="mb-4">
                  <h3 className="text-lg font-medium text-gray-900 mb-2">暂无请求</h3>
                  <p className="text-gray-500">您还没有提交任何服务请求</p>
                </div>
              }
            >
              <Link href="/service-catalog">
                <Button type="primary" icon={<FileText className="w-4 h-4" />}>
                  浏览服务目录
                </Button>
              </Link>
            </Empty>
          </Card>
        )}

        {/* 分页 */}
        {totalPages > 1 && (
          <Card
            className="mt-8 rounded-lg shadow-sm border border-gray-200"
           
            styles={{ body: { padding: '16px 24px' } }}
          >
            <div className="flex items-center justify-between">
              <div className="text-sm text-gray-500">
                显示第 {(currentPage - 1) * pageSize + 1} -{' '}
                {Math.min(currentPage * pageSize, total)} 条，共 {total} 条记录
              </div>
              <Pagination
                current={currentPage}
                total={total}
                pageSize={pageSize}
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

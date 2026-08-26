'use client';

/**
 * 工单详情组件
 * 从 tickets/[ticketId]/page.tsx 抽取，与 IncidentDetail/ProblemDetail/ChangeDetail 域组件模式对齐
 * 包含：基本信息、SLA、审批/拒绝/分配/编辑/抄送/删除操作、详情 Tabs（评论/附件/审批链/历史/关联）
 */

import React, { useState, useEffect, useCallback } from 'react';
import { useParams } from 'next/navigation';
import { TicketApi } from '@/lib/api/ticket-api';
import { TicketApprovalApi } from '@/lib/api/ticket-approval-api';
import { TicketRelationsApi } from '@/lib/api/ticket-relations-api';
import { UserApi } from '@/lib/api/user-api';
import type { Ticket } from '@/lib/api/api-config';
import type { User } from '@/lib/api/user-api';
import type { TicketPriority } from '@/types/ticket';
import {
  ArrowLeft,
  AlertCircle,
  XCircle,
  CheckCircle2,
  UserCheck,
  Edit,
  Save,
  X,
  Trash2,
  XIcon,
  Users,
  Clock,
} from 'lucide-react';
import Link from 'next/link';
import {
  Button,
  Card,
  Typography,
  App,
  Tag,
  Space,
  Modal,
  Form,
  Progress,
  Select,
  Input,
  Tabs,
  Skeleton,
} from 'antd';
import { useAuthStore } from '@/lib/store/auth-store';
import { useErrorHandler } from '@/lib/hooks/useErrorHandler';
import { formatDateTime } from '@/lib/formatters';
import { SafeTextBlock } from '@/components/common/SafeContent';
import { AISuggestionPanel } from '@/components/business/AISuggestionPanel';
import {
  isValidTransition,
  isFinalStatus,
} from '@/lib/utils/workflow-state-machine';
import {
  TicketStatus,
  TicketStatusConfig,
  getPriorityConfig,
} from '@/constants/taxonomy';
import {
  ticketCommentAdapter,
  ticketAttachmentAdapter,
} from '@/components/business/detail-tabs';
import { ApprovalMiniStepper } from '@/components/business/detail-tabs/ApprovalMiniStepper';
import ServiceRequestPanel from './ServiceRequestPanel';
import ServiceCatalogApprovalChain from './ServiceCatalogApprovalChain';
import { CIContextCard } from './CIContextCard';
import { KBRecommendCard } from './KBRecommendCard';
import { TicketCommentStream } from './TicketCommentStream';
import { TicketAttachmentGrid } from './TicketAttachmentGrid';
import { TicketHistoryList } from './TicketHistoryList';
import { TicketApprovalCards } from './TicketApprovalCards';
import { TicketRelationCards } from './TicketRelationCards';
import {
  MessageSquare,
  Paperclip,
  History as HistoryIcon,
  GitBranch,
  Link2,
  Info,
} from 'lucide-react';

const { Title, Text } = Typography;
const { TextArea } = Input;

const ticketPriorities: TicketPriority[] = ['low', 'medium', 'high', 'urgent', 'critical'];
const toTicketPriority = (value: string): TicketPriority =>
  ticketPriorities.includes(value as TicketPriority) ? (value as TicketPriority) : 'medium';

// 状态文案以 @/constants/taxonomy 为单一事实源，避免在详情页再维护一份字典。
// taxonomy 未覆盖的旧状态（approved/assigned）在此补充，不做全量复制。
const EXTRA_STATUS_CONFIG: Record<
  string,
  { label: string; badgeStatus: 'default' | 'processing' | 'success' | 'warning' | 'error' }
> = {
  approved: { label: '已批准', badgeStatus: 'processing' },
  assigned: { label: '已分配', badgeStatus: 'processing' },
};

const getTicketStatusLabel = (status?: string): string => {
  if (!status) return '';
  const config = TicketStatusConfig[status as TicketStatus] ?? EXTRA_STATUS_CONFIG[status];
  return config?.label ?? status;
};

// 根据 SLA 总时长与剩余时长计算已消耗百分比（0-100，越接近 100 越紧迫）
const getSLAPercent = (total: number, remaining: number | null): number => {
  if (!total || total <= 0 || remaining === null) return 0;
  return Math.min(100, Math.max(0, Math.round(((total - remaining) / total) * 100)));
};

const formatHours = (minutes: number): string => (minutes / 60).toFixed(1);

const DISABLED_ACTION_CLASS = 'opacity-40 cursor-not-allowed pointer-events-auto';

export const TicketDetail: React.FC<{ id?: string }> = ({ id: propId }) => {
  const params = useParams();
  // 支持通过 props 传入 id，或通过 useParams 获取
  const ticketId = parseInt((propId ?? (params?.ticketId as string)) || '');
  const currentUser = useAuthStore(state => state.user);
  const hasPermission = useAuthStore(state => state.hasPermission);
  const { message: antMessage } = App.useApp();
  const { handleError } = useErrorHandler();

  const [ticket, setTicket] = useState<Ticket | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [assignModalVisible, setAssignModalVisible] = useState(false);
  const [editModalVisible, setEditModalVisible] = useState(false);
  const [ccModalVisible, setCCModalVisible] = useState(false);
  const [deleteModalVisible, setDeleteModalVisible] = useState(false);
  const [users, setUsers] = useState<User[]>([]);
  const [loadingUsers, setLoadingUsers] = useState(false);
  const [assigning, setAssigning] = useState(false);
  const [updating, setUpdating] = useState(false);
  const [ccing, setCCing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [approving, setApproving] = useState(false);
  const [rejecting, setRejecting] = useState(false);
  const [slaInfo, setSlaInfo] = useState<{
    slaName: string;
    responseTime: number;
    resolutionTime: number;
    responseDeadline: string | null;
    resolutionDeadline: string | null;
    responseTimeRemaining: number | null;
    resolutionTimeRemaining: number | null;
    isBreached: boolean;
  } | null>(null);
  const [tabCounts, setTabCounts] = useState<{
    comments?: number;
    attachments?: number;
    approvals?: number;
    history?: number;
    relations?: number;
  }>({});

  const [assignForm] = Form.useForm();
  const [editForm] = Form.useForm();
  const [ccForm] = Form.useForm();

  // Get ticket details
  const fetchTicket = useCallback(async () => {
    // Skip if ticketId is not a valid number
    if (!ticketId || isNaN(ticketId) || ticketId <= 0) {
      setError('无效的工单ID');
      setLoading(false);
      return;
    }

    try {
      setLoading(true);
      setError(null);
      const data = await TicketApi.getTicket(ticketId);
      setTicket(data);
    } catch (error) {
      handleError(error, 'fetchTicket', '获取工单详情失败');
      setError(error instanceof Error ? error.message : 'Network error');
    } finally {
      setLoading(false);
    }
  }, [ticketId, handleError]);

  // Get users for assignment
  const fetchUsers = useCallback(async () => {
    if (!hasPermission('user:read')) {
      setUsers([]);
      return;
    }
    try {
      setLoadingUsers(true);
      const data = await UserApi.getUsers({ pageSize: 100 });
      setUsers(data.users || []);
    } catch (error) {
      setUsers([]);
    } finally {
      setLoadingUsers(false);
    }
  }, [hasPermission]);

  // Get ticket SLA info
  const fetchSLAInfo = useCallback(async () => {
    try {
      const data = await TicketApi.getTicketSLA(ticketId);
      setSlaInfo(data);
    } catch (error) {
      setSlaInfo(null);
    }
  }, [ticketId]);

  useEffect(() => {
    if (ticketId) {
      fetchTicket();
    }
  }, [ticketId, fetchTicket]);

  useEffect(() => {
    if (ticketId) {
      fetchSLAInfo();
    }
  }, [ticketId, fetchSLAInfo]);

  // 底部五维 Tabs 的数量角标（失败静默，不阻塞详情页主流程）
  useEffect(() => {
    if (!ticketId) return;

    let cancelled = false;
    (async () => {
      try {
        const [comments, attachments, approvals, history, relations] = await Promise.allSettled([
          ticketCommentAdapter.list(ticketId),
          ticketAttachmentAdapter.list(ticketId),
          TicketApprovalApi.getApprovalDecisions(ticketId),
          TicketApi.getTicketHistory(ticketId),
          TicketRelationsApi.getRelationStats(ticketId),
        ]);
        if (cancelled) return;

        const next: {
          comments?: number;
          attachments?: number;
          approvals?: number;
          history?: number;
          relations?: number;
        } = {};
        if (comments.status === 'fulfilled' && typeof comments.value?.total === 'number') {
          next.comments = comments.value.total;
        }
        if (attachments.status === 'fulfilled' && Array.isArray(attachments.value)) {
          next.attachments = attachments.value.length;
        }
        if (approvals.status === 'fulfilled' && Array.isArray(approvals.value)) {
          next.approvals = approvals.value.length;
        }
        if (history.status === 'fulfilled' && Array.isArray(history.value)) {
          next.history = history.value.length;
        }
        if (relations.status === 'fulfilled' && typeof relations.value?.totalRelations === 'number') {
          next.relations = relations.value.totalRelations;
        }
        setTabCounts(next);
      } catch {
        // 任一数据源异常都静默处理，不阻塞详情页
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [ticketId]);

  useEffect(() => {
    fetchUsers();
  }, [fetchUsers]);

  // Handle approval (轻量版：仅改状态)
  const handleApprove = async () => {
    if (approving) return;
    try {
      setApproving(true);
      await TicketApi.updateTicketStatus(ticketId, 'approved');
      antMessage.success('批准成功');
      fetchTicket();
    } catch (error) {
      handleError(error, 'approveTicket', '批准失败');
    } finally {
      setApproving(false);
    }
  };

  const handleCCSubmit = async (values: {
    ccUsers: number[];
    comment?: string;
    notifyChannels?: string[];
  }) => {
    try {
      setCCing(true);
      await TicketApi.ccTicket(
        ticketId,
        values.ccUsers,
        values.comment,
        values.notifyChannels || ['in_app']
      );
      antMessage.success('抄送成功');
      setCCModalVisible(false);
      ccForm.resetFields();
      fetchTicket();
    } catch (error) {
      handleError(error, 'ccTicket', '抄送失败');
    } finally {
      setCCing(false);
    }
  };

  // Handle rejection (轻量版：仅改状态)
  const handleReject = async () => {
    if (rejecting) return;
    try {
      setRejecting(true);
      await TicketApi.updateTicketStatus(ticketId, 'rejected');
      antMessage.success('已拒绝');
      fetchTicket();
    } catch (error) {
      handleError(error, 'rejectTicket', '拒绝失败');
    } finally {
      setRejecting(false);
    }
  };

  // Handle assignment
  const handleAssign = () => {
    setAssignModalVisible(true);
  };

  // Handle assignment submit
  const handleAssignSubmit = async (values: { assigneeId: number; comment?: string }) => {
    try {
      setAssigning(true);
      await TicketApi.assignTicket(ticketId, values);
      antMessage.success('工单分配成功');
      setAssignModalVisible(false);
      assignForm.resetFields();
      fetchTicket();
    } catch (error) {
      handleError(error, 'assignTicket', '分配失败');
    } finally {
      setAssigning(false);
    }
  };

  // Handle edit
  const handleUpdate = () => {
    if (ticket) {
      editForm.setFieldsValue({
        title: ticket.title,
        description: ticket.description,
        priority: ticket.priority,
        status: ticket.status,
      });
      setEditModalVisible(true);
    }
  };

  // Handle edit submit
  const handleEditSubmit = async (values: Partial<Ticket>) => {
    try {
      setUpdating(true);
      // 状态转换验证
      if (values.status && ticket?.status && values.status !== ticket.status) {
        if (!isValidTransition(ticket.status as any, values.status as any)) {
          antMessage.error(
            `不允许从 "${getTicketStatusLabel(ticket.status)}" 转换到 "${getTicketStatusLabel(values.status)}"`
          );
          return;
        }
      }

      // 添加版本号用于乐观锁
      const updatePayload = {
        ...values,
        version: ticket?.version,
      };

      await TicketApi.updateTicket(ticketId, updatePayload);
      antMessage.success('工单更新成功');
      setEditModalVisible(false);
      fetchTicket();
    } catch (error) {
      handleError(error, 'updateTicket', '更新失败');
    } finally {
      setUpdating(false);
    }
  };

  // Handle delete click
  const handleDeleteClick = () => {
    setDeleteModalVisible(true);
  };

  // Handle delete confirm
  const handleDeleteConfirm = async () => {
    try {
      setDeleting(true);
      await TicketApi.deleteTicket(ticketId);
      antMessage.success('工单删除成功');
      setDeleteModalVisible(false);
      // Navigate back to ticket list
      window.location.href = '/tickets';
    } catch (error) {
      handleError(error, 'deleteTicket', '删除失败');
    } finally {
      setDeleting(false);
    }
  };

  // 工单操作快捷键：Alt+R 刷新，Alt+E 编辑，Esc 关闭当前弹窗。
  useEffect(() => {
    const handleShortcut = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (target?.closest('input, textarea, select, [contenteditable="true"]')) return;

      if (event.key === 'Escape') {
        setAssignModalVisible(false);
        setEditModalVisible(false);
        setCCModalVisible(false);
        setDeleteModalVisible(false);
        return;
      }
      if (!event.altKey) return;
      if (event.key.toLowerCase() === 'r') {
        event.preventDefault();
        fetchTicket();
      } else if (event.key.toLowerCase() === 'e' && ticket && ticket.actions?.edit?.allowed) {
        event.preventDefault();
        editForm.setFieldsValue({
          title: ticket.title,
          description: ticket.description,
          priority: ticket.priority,
          status: ticket.status,
        });
        setEditModalVisible(true);
      }
    };

    window.addEventListener('keydown', handleShortcut);
    return () => window.removeEventListener('keydown', handleShortcut);
  }, [editForm, fetchTicket, ticket]);

  if (loading) {
    return (
      <div className="p-6">
        <Card>
          <Skeleton active title={{ width: '45%' }} paragraph={{ rows: 10 }} />
        </Card>
      </div>
    );
  }

  if (error) {
    return (
      <div className="p-6">
        <Card>
          <div className="text-center py-8">
            <AlertCircle className="w-12 h-12 text-red-500 mx-auto mb-4" />
            <Title level={4} className="text-red-600 mb-2">
              加载失败
            </Title>
            <Text type="secondary">{error}</Text>
            <div className="mt-4">
              <Button type="primary" onClick={fetchTicket}>
                重试
              </Button>
            </div>
          </div>
        </Card>
      </div>
    );
  }

  if (!ticket) {
    return (
      <div className="p-6">
        <Card>
          <div className="text-center py-8">
            <XCircle className="w-12 h-12 text-gray-400 mx-auto mb-4" />
            <Title level={4} className="text-gray-600 mb-2">
              未找到工单
            </Title>
            <Text type="secondary">未找到指定的工单</Text>
            <div className="mt-4">
              <Link href="/tickets">
                <Button type="primary">返回工单列表</Button>
              </Link>
            </div>
          </div>
        </Card>
      </div>
    );
  }

  const isTicketFinal = isFinalStatus(ticket.status as any);

  return (
    <div className="w-full space-y-4 text-slate-800 font-sans antialiased">
      {/* ================= 工单主 Header & 规范动作控制台 ================= */}
      <div className="w-full bg-white rounded-2xl border border-slate-200/90 p-4 sm:p-5 shadow-xs">
        <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-4">
          {/* 左侧：返回、单号、标题、Tag */}
          <div className="space-y-2 min-w-0">
            <div className="flex items-center gap-2 text-xs text-slate-500">
              <Link href="/tickets" className="inline-flex items-center gap-1 text-slate-500 hover:text-slate-900 font-medium transition-colors">
                <ArrowLeft size={13} />
                返回工单列表
              </Link>
              <span>/</span>
              <span className="font-mono text-slate-400">{ticket.ticketNumber || `#${ticket.id}`}</span>
              {ticket.source && (
                <>
                  <span>/</span>
                  <span className="text-slate-600">
                    {ticket.source === 'service_catalog' ? '服务目录申请' : ticket.source}
                  </span>
                </>
              )}
            </div>

            <div className="flex flex-wrap items-center gap-2.5">
              <h1 className="text-lg sm:text-xl font-bold text-slate-900 tracking-tight m-0 truncate">
                #{ticket.id} {ticket.title}
              </h1>
              <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-semibold bg-orange-50 text-orange-700 border border-orange-200">
                <span className="w-1.5 h-1.5 rounded-full bg-orange-500 mr-1.5" />
                {getTicketStatusLabel(ticket.status)}
              </span>
              <span className="inline-flex items-center px-2 py-0.5 rounded-md text-xs font-medium bg-slate-100 text-slate-700 border border-slate-200">
                {getPriorityConfig(ticket.priority).label}
              </span>
            </div>
          </div>

          {/* 右侧：规范动作按钮控制台 */}
          <div className="flex flex-wrap items-center gap-2 self-start lg:self-center shrink-0">
            <button
              type="button"
              onClick={handleApprove}
              disabled={!ticket.actions?.approve?.allowed}
              title={ticket.actions?.approve?.reason || ''}
              className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-orange-500 hover:bg-orange-600 active:bg-orange-700 text-white transition-colors duration-150 cursor-pointer shadow-xs disabled:opacity-50 disabled:cursor-not-allowed ${
                !ticket.actions?.approve?.allowed ? DISABLED_ACTION_CLASS : ''
              }`}
            >
              <CheckCircle2 size={13} />
              <span>{approving ? '批准中...' : '批准'}</span>
            </button>

            <button
              type="button"
              onClick={handleReject}
              disabled={!ticket.actions?.reject?.allowed}
              title={ticket.actions?.reject?.reason || ''}
              className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white hover:bg-slate-50 text-slate-700 border border-slate-200 hover:border-slate-300 transition-colors duration-150 cursor-pointer shadow-2xs disabled:opacity-50 disabled:cursor-not-allowed ${
                !ticket.actions?.reject?.allowed ? DISABLED_ACTION_CLASS : ''
              }`}
            >
              <XIcon size={13} className="text-slate-500" />
              <span>{rejecting ? '拒绝中...' : '拒绝'}</span>
            </button>

            <button
              type="button"
              onClick={handleAssign}
              disabled={!ticket.actions?.assign?.allowed}
              title={ticket.actions?.assign?.reason || ''}
              className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white hover:bg-slate-50 text-slate-700 border border-slate-200 hover:border-slate-300 transition-colors duration-150 cursor-pointer shadow-2xs disabled:opacity-50 disabled:cursor-not-allowed ${
                !ticket.actions?.assign?.allowed ? DISABLED_ACTION_CLASS : ''
              }`}
            >
              <UserCheck size={13} className="text-slate-500" />
              <span>转派分配</span>
            </button>

            <button
              type="button"
              onClick={handleUpdate}
              disabled={!ticket.actions?.edit?.allowed}
              title={ticket.actions?.edit?.reason || ''}
              className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white hover:bg-slate-50 text-slate-700 border border-slate-200 hover:border-slate-300 transition-colors duration-150 cursor-pointer shadow-2xs disabled:opacity-50 disabled:cursor-not-allowed ${
                !ticket.actions?.edit?.allowed ? DISABLED_ACTION_CLASS : ''
              }`}
            >
              <Edit size={13} className="text-slate-500" />
              <span>编辑</span>
            </button>

            <button
              type="button"
              onClick={() => setCCModalVisible(true)}
              disabled={!ticket.actions?.cc?.allowed}
              title={ticket.actions?.cc?.reason || ''}
              className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white hover:bg-slate-50 text-slate-700 border border-slate-200 hover:border-slate-300 transition-colors duration-150 cursor-pointer shadow-2xs disabled:opacity-50 disabled:cursor-not-allowed ${
                !ticket.actions?.cc?.allowed ? DISABLED_ACTION_CLASS : ''
              }`}
            >
              <Users size={13} className="text-slate-500" />
              <span>抄送</span>
            </button>

            <button
              type="button"
              onClick={handleDeleteClick}
              disabled={!ticket.actions?.delete?.allowed}
              title={ticket.actions?.delete?.reason || ''}
              className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white hover:bg-red-50 text-red-600 border border-red-200 hover:border-red-300 transition-colors duration-150 cursor-pointer shadow-2xs disabled:opacity-50 disabled:cursor-not-allowed ${
                !ticket.actions?.delete?.allowed ? DISABLED_ACTION_CLASS : ''
              }`}
            >
              <Trash2 size={13} className="text-red-500" />
              <span>删除</span>
            </button>
          </div>
        </div>

        {isTicketFinal && (
          <div className="mt-3 pt-2 border-t border-slate-100 text-xs text-slate-400">
            工单已结束，当前处于只读归档状态。
          </div>
        )}
      </div>

      {/* ================= 主体工作台栅格: 左侧 8 列 + 右侧 4 列 ================= */}
      <div className="w-full grid grid-cols-1 lg:grid-cols-12 gap-5 items-start">
        {/* 左侧 8 列: 诉求描述 -> 服务目录交付规格 -> 底部五维 Tabs 协作流 */}
        <div className="lg:col-span-8 space-y-5 min-w-0">
          {/* 1. 核心诉求描述卡片 */}
          <div className="bg-white rounded-2xl border border-slate-200/90 p-5 shadow-xs space-y-4">
            <div className="flex items-center justify-between border-b border-slate-100 pb-3">
              <div className="flex items-center gap-2">
                <span className="font-bold text-sm text-slate-800">工单诉求与业务描述</span>
                <span className="text-[11px] text-slate-400">申请人填写</span>
              </div>
              <span className="text-xs font-mono text-slate-400">
                提交于 {formatDateTime(ticket.createdAt)}
              </span>
            </div>

            <div className="text-xs text-slate-700 leading-relaxed whitespace-pre-line bg-slate-50/70 p-4 rounded-xl border border-slate-100">
              <SafeTextBlock content={ticket.description} fallback="暂无详细描述" />
            </div>

            {/* 动态自定义字段网格展示 */}
            {ticket.customFields && ticket.customFields.length > 0 && (
              <div className="pt-2 border-t border-slate-100 space-y-2">
                <span className="text-xs font-bold text-slate-700 block">业务扩展参数</span>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2.5 text-xs">
                  {ticket.customFields.map(field => (
                    <div key={field.name} className="p-2.5 bg-slate-50 rounded-lg border border-slate-100">
                      <span className="text-slate-400 block text-[11px]">{field.label}:</span>
                      <span className="font-medium text-slate-800 break-words">{String(field.value)}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>

          {/* 2. 服务目录专属交付面板 */}
          {ticket?.source === 'service_catalog' && (
            <div className="bg-white rounded-2xl border border-slate-200/90 p-5 shadow-xs">
              <ServiceRequestPanel ticketId={ticket.id} />
            </div>
          )}

          {/* 3. 底部五维完整 Tabs（评论/附件/审批链/历史/关联） */}
          <TicketDetailTabs
            ticketId={ticketId}
            ticketSource={ticket.source}
            currentUserId={currentUser?.id}
            ticketAssigneeId={ticket.assigneeId}
            tabCounts={tabCounts}
          />
        </div>

        {/* 右侧 4 列: 【高密度运维工具箱 + 悬浮跟随】 */}
        <div className="lg:col-span-4 space-y-4 sticky top-4 min-w-0">
          {/* 1. 工单上下文属性 (置顶) */}
          <div className="bg-white rounded-2xl border border-slate-200/90 p-4 shadow-xs space-y-3 text-xs">
            <span className="font-bold text-slate-800 block border-b border-slate-100 pb-2 text-xs">
              工单上下文属性
            </span>

            <div className="space-y-2.5">
              <div className="flex items-center justify-between">
                <span className="text-slate-400 text-xs">申请人:</span>
                <span className="font-medium text-slate-800 text-xs">
                  {ticket.requester?.name || '-'}
                  {ticket.requester?.username ? ` (${ticket.requester.username})` : ''}
                </span>
              </div>

              <div className="flex items-center justify-between">
                <span className="text-slate-400 text-xs">所属部门:</span>
                <span className="text-slate-700 text-xs">{ticket.requester?.department || '-'}</span>
              </div>

              <div className="flex items-center justify-between">
                <span className="text-slate-400 text-xs">当前处理人:</span>
                <span className="font-semibold text-orange-800 bg-orange-50 px-2 py-0.5 rounded border border-orange-200 text-xs">
                  {ticket.assignee?.name || '未分配'}
                </span>
              </div>

              <div className="flex items-center justify-between">
                <span className="text-slate-400 text-xs">工单分类:</span>
                <span className="text-slate-700 text-xs">{ticket.category || '未分类'}</span>
              </div>
            </div>
          </div>

          {/* 2. ⚡ AI 智能辅助建议卡片 */}
          <AISuggestionPanel
            title={ticket.title}
            description={ticket.description}
            onAccept={async suggestion => {
              if (
                suggestion.priority === ticket.priority &&
                suggestion.category === ticket.category
              ) {
                antMessage.info('AI建议与当前分类/优先级一致，无需更新');
                return;
              }
              try {
                const updated = await TicketApi.updateTicket(ticketId, {
                  category: suggestion.category,
                  priority: toTicketPriority(suggestion.priority),
                  version: ticket.version,
                } as any);
                antMessage.success(
                  `已采纳AI建议：分类 ${suggestion.category}，优先级 ${suggestion.priority}`,
                );
                if (updated && (updated as any).id) {
                  setTicket(prev => (prev ? { ...prev, ...(updated as Partial<Ticket>) } : prev));
                }
                await fetchTicket();
              } catch (err) {
                handleError(err, 'applyAISuggestion', '采纳建议失败');
              }
            }}
          />

          {/* 2.5 流转节点进度（Mini BPMN Stepper） */}
          <ApprovalMiniStepper ticketId={ticketId} />

          {/* 3. SLA 履约时限监控卡片 */}
          {slaInfo && (
            <div className="bg-white rounded-2xl border border-slate-200/90 p-4 shadow-xs space-y-3">
              <div className="flex items-center justify-between">
                <span className="text-xs font-bold text-slate-800 flex items-center gap-1.5">
                  <Clock size={14} className="text-slate-500" />
                  SLA 时效与承诺
                </span>
                <Tag color={slaInfo.isBreached ? 'red' : 'blue'}>{slaInfo.slaName}</Tag>
              </div>

              <div className="bg-slate-50 p-3 rounded-xl border border-slate-100 space-y-2 text-xs">
                {slaInfo.responseDeadline && (
                  <div className="flex items-center justify-between">
                    <span className="text-slate-500 text-[11px]">响应截止:</span>
                    <span
                      className={`font-mono text-xs ${
                        slaInfo.responseTimeRemaining !== null && slaInfo.responseTimeRemaining < 0
                          ? 'text-red-600 font-bold'
                          : 'text-slate-800'
                      }`}
                    >
                      {new Date(slaInfo.responseDeadline).toLocaleString()}
                      {slaInfo.responseTimeRemaining !== null &&
                        slaInfo.responseTimeRemaining < 0 &&
                        ' (已超时)'}
                    </span>
                  </div>
                )}

                {slaInfo.resolutionDeadline && (
                  <div className="flex items-center justify-between">
                    <span className="text-slate-500 text-[11px]">解决截止:</span>
                    <span
                      className={`font-mono text-xs ${
                        slaInfo.resolutionTimeRemaining !== null && slaInfo.resolutionTimeRemaining < 0
                          ? 'text-red-600 font-bold'
                          : 'text-slate-800'
                      }`}
                    >
                      {new Date(slaInfo.resolutionDeadline).toLocaleString()}
                      {slaInfo.resolutionTimeRemaining !== null &&
                        slaInfo.resolutionTimeRemaining < 0 &&
                        ' (已超时)'}
                    </span>
                  </div>
                )}

                {slaInfo.isBreached && (
                  <div className="pt-1">
                    <Tag color="red" className="w-full text-center">
                      SLA 已违规
                    </Tag>
                  </div>
                )}

                {slaInfo.responseTime > 0 && (
                  <div className="space-y-1">
                    <div className="flex justify-between text-[11px] text-slate-500">
                      <span>响应进度</span>
                      <span>
                        {slaInfo.responseTimeRemaining !== null
                          ? `剩余 ${slaInfo.responseTimeRemaining} 分钟`
                          : '--'}
                      </span>
                    </div>
                    <Progress
                      percent={getSLAPercent(slaInfo.responseTime, slaInfo.responseTimeRemaining)}
                      size="small"
                      strokeColor={
                        slaInfo.responseTimeRemaining !== null && slaInfo.responseTimeRemaining < 0
                          ? '#ff4d4f'
                          : getSLAPercent(slaInfo.responseTime, slaInfo.responseTimeRemaining) >= 70
                            ? '#fa8c16'
                            : '#52c41a'
                      }
                    />
                    <div className="flex justify-between text-[11px] text-slate-400 font-mono">
                      <span>
                        {slaInfo.responseTimeRemaining !== null
                          ? `剩余 ${formatHours(slaInfo.responseTimeRemaining)} 小时`
                          : '--'}
                      </span>
                      <span>目标 {formatHours(slaInfo.responseTime)} 小时</span>
                    </div>
                  </div>
                )}

                {slaInfo.resolutionTime > 0 && (
                  <div className="space-y-1">
                    <div className="flex justify-between text-[11px] text-slate-500">
                      <span>解决进度</span>
                      <span>
                        {slaInfo.resolutionTimeRemaining !== null
                          ? `剩余 ${slaInfo.resolutionTimeRemaining} 分钟`
                          : '--'}
                      </span>
                    </div>
                    <Progress
                      percent={getSLAPercent(slaInfo.resolutionTime, slaInfo.resolutionTimeRemaining)}
                      size="small"
                      strokeColor={
                        slaInfo.resolutionTimeRemaining !== null && slaInfo.resolutionTimeRemaining < 0
                          ? '#ff4d4f'
                          : getSLAPercent(slaInfo.resolutionTime, slaInfo.resolutionTimeRemaining) >= 70
                            ? '#fa8c16'
                            : '#52c41a'
                      }
                    />
                    <div className="flex justify-between text-[11px] text-slate-400 font-mono">
                      <span>
                        {slaInfo.resolutionTimeRemaining !== null
                          ? `剩余 ${formatHours(slaInfo.resolutionTimeRemaining)} 小时`
                          : '--'}
                      </span>
                      <span>目标 {formatHours(slaInfo.resolutionTime)} 小时</span>
                    </div>
                  </div>
                )}
              </div>
            </div>
          )}

          {/* 4. 关联 CMDB 配置项（CI）卡片 */}
          <CIContextCard ticketId={ticketId} source={ticket.source} />

          {/* 5. 推荐操作指引 (KB) */}
          <KBRecommendCard query={ticket.title} />
        </div>
      </div>

      {/* ================= 业务操作弹窗集群（零丢失） ================= */}
      {/* 1. Assignment Modal */}
      <Modal
        title={
          <Space>
            <UserCheck className="w-5 h-5 text-blue-600" />
            分配工单
          </Space>
        }
        open={assignModalVisible}
        onCancel={() => {
          setAssignModalVisible(false);
          assignForm.resetFields();
        }}
        footer={null}
        width={500}
      >
        <Form form={assignForm} layout="vertical" onFinish={handleAssignSubmit}>
          <Form.Item
            label="分配给"
            name="assigneeId"
            rules={[{ required: true, message: '请选择处理人' }]}
          >
            <Select
              placeholder="请选择处理人"
              loading={loadingUsers}
              showSearch
              filterOption={(input, option) =>
                (option?.label as unknown as string)?.toLowerCase().includes(input.toLowerCase())
              }
              options={users.map(user => ({
                value: user.id,
                label: (
                  <Space>
                    <span>{user.name}</span>
                    <Text type="secondary" className="text-xs">
                      ({user.username})
                    </Text>
                    {user.department && <Tag color="blue">{user.department}</Tag>}
                  </Space>
                ),
              }))}
            />
          </Form.Item>
          <Form.Item label="备注" name="comment">
            <TextArea rows={3} placeholder="请输入分配备注（可选）" maxLength={500} showCount />
          </Form.Item>
          <Form.Item className="mb-0">
            <Space className="w-full justify-end">
              <Button
                icon={<X />}
                onClick={() => {
                  setAssignModalVisible(false);
                  assignForm.resetFields();
                }}
              >
                取消
              </Button>
              <Button type="primary" htmlType="submit" icon={<Save />} loading={assigning}>
                确认分配
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Modal>

      {/* 2. Edit Modal */}
      <Modal
        title={
          <Space>
            <Edit className="w-5 h-5 text-green-600" />
            编辑工单
          </Space>
        }
        open={editModalVisible}
        onCancel={() => {
          setEditModalVisible(false);
          editForm.resetFields();
        }}
        footer={null}
        width={600}
      >
        <Form form={editForm} layout="vertical" onFinish={handleEditSubmit}>
          <Form.Item
            label="工单标题"
            name="title"
            rules={[
              { required: true, message: '请输入工单标题' },
              { max: 100, message: '标题不能超过100个字符' },
            ]}
          >
            <Input placeholder="请输入工单标题" />
          </Form.Item>
          <Form.Item
            label="工单描述"
            name="description"
            rules={[
              { required: true, message: '请输入工单描述' },
              { max: 2000, message: '描述不能超过2000个字符' },
            ]}
          >
            <TextArea rows={6} placeholder="请输入工单描述" showCount maxLength={2000} />
          </Form.Item>
          <div className="grid grid-cols-2 gap-4">
            <Form.Item
              label="优先级"
              name="priority"
              rules={[{ required: true, message: '请选择优先级' }]}
            >
              <Select
                placeholder="请选择优先级"
                options={[
                  {
                    value: 'low',
                    label: <Tag color="green">低优先级</Tag>,
                  },
                  {
                    value: 'medium',
                    label: <Tag color="orange">中优先级</Tag>,
                  },
                  {
                    value: 'high',
                    label: <Tag color="red">高优先级</Tag>,
                  },
                ]}
              />
            </Form.Item>
            <Form.Item
              label="状态"
              name="status"
              rules={[{ required: true, message: '请选择状态' }]}
              extra={ticket ? `当前状态: ${getTicketStatusLabel(ticket.status)}` : ''}
            >
              <Select
                placeholder="请选择状态"
                options={[
                  { value: 'new', label: '待处理' },
                  { value: 'in_progress', label: '处理中' },
                  { value: 'pending', label: '暂停' },
                  { value: 'resolved', label: '已解决' },
                  { value: 'closed', label: '已关闭' },
                ]}
              />
            </Form.Item>
          </div>
          <Form.Item className="mb-0">
            <Space className="w-full justify-end">
              <Button
                icon={<X />}
                onClick={() => {
                  setEditModalVisible(false);
                  editForm.resetFields();
                }}
              >
                取消
              </Button>
              <Button type="primary" htmlType="submit" icon={<Save />} loading={updating}>
                保存修改
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Modal>

      {/* 3. CC Modal */}
      <Modal
        title={
          <Space>
            <Users className="w-5 h-5 text-blue-600" />
            抄送工单
          </Space>
        }
        open={ccModalVisible}
        onCancel={() => {
          setCCModalVisible(false);
          ccForm.resetFields();
        }}
        footer={null}
        width={520}
      >
        <Form
          form={ccForm}
          layout="vertical"
          initialValues={{ notifyChannels: ['in_app'] }}
          onFinish={handleCCSubmit}
        >
          <Form.Item
            label="抄送给"
            name="ccUsers"
            rules={[{ required: true, message: '请选择抄送人' }]}
          >
            <Select
              mode="multiple"
              placeholder="请选择抄送人"
              loading={loadingUsers}
              showSearch
              optionFilterProp="label"
              options={users.map(user => ({
                value: user.id,
                label: `${user.name || user.username}${user.department ? ` (${user.department})` : ''}`,
              }))}
            />
          </Form.Item>
          <Form.Item label="通知渠道" name="notifyChannels">
            <Select
              mode="multiple"
              placeholder="请选择通知渠道"
              options={[
                { value: 'in_app', label: '站内信' },
                { value: 'email', label: '邮件' },
                { value: 'sms', label: '短信' },
                { value: 'feishu', label: '飞书' },
                { value: 'dingtalk', label: '钉钉' },
                { value: 'wecom', label: '企业微信' },
                { value: 'webhook', label: 'Webhook' },
              ]}
            />
          </Form.Item>
          <Form.Item label="备注" name="comment">
            <TextArea rows={3} placeholder="请输入抄送备注（可选）" maxLength={500} showCount />
          </Form.Item>
          <Form.Item className="mb-0">
            <Space className="w-full justify-end">
              <Button
                icon={<X />}
                onClick={() => {
                  setCCModalVisible(false);
                  ccForm.resetFields();
                }}
              >
                取消
              </Button>
              <Button type="primary" htmlType="submit" icon={<Users />} loading={ccing}>
                确认抄送
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Modal>

      {/* 4. Delete Confirmation Modal */}
      <Modal
        title={
          <Space>
            <Trash2 className="w-5 h-5 text-red-600" />
            删除工单
          </Space>
        }
        open={deleteModalVisible}
        onCancel={() => setDeleteModalVisible(false)}
        footer={null}
        width={400}
      >
        <div className="py-4">
          <div className="flex items-start gap-3 mb-4">
            <AlertCircle className="w-6 h-6 text-red-500 flex-shrink-0 mt-0.5" />
            <div>
              <Typography.Text strong className="text-lg">
                确定要删除此工单吗？
              </Typography.Text>
              <Typography.Paragraph type="secondary" className="mb-0 mt-1">
                此操作不可恢复，工单编号 #{ticket.id} 将被永久删除。
              </Typography.Paragraph>
            </div>
          </div>
          <div className="bg-gray-50 rounded p-3 mb-4">
            <Typography.Text type="secondary" className="text-sm">
              工单信息：
            </Typography.Text>
            <div className="mt-1">
              <Text strong>{ticket.title}</Text>
            </div>
          </div>
        </div>
        <Space className="w-full justify-end">
          <Button onClick={() => setDeleteModalVisible(false)} disabled={deleting}>
            取消
          </Button>
          <Button
            danger
            type="primary"
            onClick={handleDeleteConfirm}
            loading={deleting}
            icon={<Trash2 size={14} />}
          >
            确认删除
          </Button>
        </Space>
      </Modal>
    </div>
  );
};

// ==================== 详情 Tabs 子组件 ====================

interface TicketDetailTabsProps {
  ticketId: number;
  ticketSource?: string;
  currentUserId?: number;
  ticketAssigneeId?: number;
  tabCounts?: {
    comments?: number;
    attachments?: number;
    approvals?: number;
    history?: number;
    relations?: number;
  };
}

const TicketDetailTabs: React.FC<TicketDetailTabsProps> = ({
  ticketId,
  ticketSource,
  currentUserId,
  tabCounts,
  ticketAssigneeId,
}) => {
  const countSuffix = (count?: number) => (count !== undefined ? ` (${count})` : '');

  const items = [
    {
      key: 'comments',
      label: (
        <span>
          <MessageSquare size={14} className="inline mr-1" />
          协作沟通与评论{countSuffix(tabCounts?.comments)}
        </span>
      ),
      children: (
        <TicketCommentStream
          ticketId={ticketId}
          currentUserId={currentUserId}
          ticketAssigneeId={ticketAssigneeId}
          formatDateTime={formatDateTime}
        />
      ),
    },
    {
      key: 'attachments',
      label: (
        <span>
          <Paperclip size={14} className="inline mr-1" />
          附件{countSuffix(tabCounts?.attachments)}
        </span>
      ),
      children: <TicketAttachmentGrid ticketId={ticketId} />,
    },
    {
      key: 'approvals',
      label: (
        <span>
          <GitBranch size={14} className="inline mr-1" />
          审批链{countSuffix(tabCounts?.approvals)}
        </span>
      ),
      children: (
        <div>
          {ticketSource === 'service_catalog' && (
            <ServiceCatalogApprovalChain ticketId={ticketId} />
          )}
          <TicketApprovalCards ticketId={ticketId} />
        </div>
      ),
    },
    {
      key: 'history',
      label: (
        <span>
          <HistoryIcon size={14} className="inline mr-1" />
          历史流转{countSuffix(tabCounts?.history)}
        </span>
      ),
      children: <TicketHistoryList ticketId={ticketId} formatDateTime={formatDateTime} />,
    },
    {
      key: 'relations',
      label: (
        <span>
          <Link2 size={14} className="inline mr-1" />
          关联工单与资产{countSuffix(tabCounts?.relations)}
        </span>
      ),
      children: <TicketRelationCards ticketId={ticketId} />,
    },
  ];

  return (
    <div className="bg-white rounded-2xl border border-slate-200/90 p-5 shadow-xs space-y-3">
      <div className="flex items-center gap-2 text-slate-500 text-xs font-semibold border-b border-slate-100 pb-2">
        <Info size={13} />
        协作流、审批链与审计历史
      </div>
      <Tabs items={items} defaultActiveKey="comments" />
    </div>
  );
};

export default TicketDetail;

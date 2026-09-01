/**
 * 工单流转相关类型定义
 */

import type { TicketStatus, TicketUser } from './ticket';

/**
 * 工单流转操作类型
 */
export enum TicketWorkflowAction {
  ACCEPT = 'accept', // 接单
  WITHDRAW = 'withdraw', // 撤回
  FORWARD = 'forward', // 转发
  CC = 'cc', // 抄送
  ESCALATE = 'escalate', // 升级
  RESOLVE = 'resolve', // 解决
  CLOSE = 'close', // 关闭
  REOPEN = 'reopen', // 重开
}

/**
 * 工单流转记录
 */
export interface TicketWorkflowRecord {
  id: number;
  ticketId: number;
  action: TicketWorkflowAction;
  fromStatus?: TicketStatus;
  toStatus?: TicketStatus;
  operator: TicketUser;
  fromUser?: TicketUser;
  toUser?: TicketUser;
  comment?: string;
  reason?: string;
  attachments?: Array<{
    id: number;
    filename: string;
    url: string;
  }>;
  metadata?: Record<string, any>;
  createdAt: string;
}

/**
 * 工单当前状态
 */
export interface TicketWorkflowState {
  ticketId: number;
  currentStatus: TicketStatus;
  currentAssignee?: TicketUser;
  canAccept: boolean;
  canWithdraw: boolean;
  canForward: boolean;
  canCC: boolean;
  canResolve: boolean;
  canClose: boolean;
  availableActions: TicketWorkflowAction[];
}

/**
 * 接单请求
 */
export interface AcceptTicketRequest {
  ticketId: number;
  comment?: string;
}

/**
 * 撤回请求
 */
export interface WithdrawTicketRequest {
  ticketId: number;
  reason: string;
}

/**
 * 转发请求
 */
export interface ForwardTicketRequest {
  ticketId: number;
  toUserId: number;
  comment?: string;
  transferOwnership: boolean; // 是否转移所有权
}

/**
 * 抄送请求
 */
export interface CCTicketRequest {
  ticketId: number;
  ccUsers: number[];
  comment?: string;
}

/**
 * 解决工单请求
 */
export interface ResolveTicketRequest {
  ticketId: number;
  resolution: string;
  resolutionCategory?: string;
  workNotes?: string;
  attachments?: File[];
}

/**
 * 关闭工单请求
 */
export interface CloseTicketRequest {
  ticketId: number;
  closeReason?: string;
  closeNotes?: string;
}

/**
 * 重开工单请求
 */
export interface ReopenTicketRequest {
  ticketId: number;
  reason: string;
}

/**
 * 抄送人
 */
export interface TicketCC {
  id: number;
  ticketId: number;
  user: TicketUser;
  addedBy: TicketUser;
  addedAt: string;
  isActive: boolean;
}

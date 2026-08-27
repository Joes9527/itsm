/**
 * TicketDetail Component Tests
 *
 * 覆盖本次工作台重构的回归面：
 * - open / assigned 等真实工单状态必须渲染中文文案（不能回退成英文原文）
 */

import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import TicketDetail from '../TicketDetail';

jest.mock('antd', () => {
  const actual = jest.requireActual('antd');
  const message = { success: jest.fn(), error: jest.fn(), info: jest.fn() };
  return {
    ...actual,
    App: { useApp: () => ({ message }) },
    message,
  };
});

jest.mock('next/navigation', () => ({
  useParams: () => ({ ticketId: '101' }),
  useRouter: () => ({ push: jest.fn() }),
}));

jest.mock('@/lib/api/ticket-api', () => ({
  TicketApi: {
    getTicket: jest.fn(),
    getTicketSLA: jest.fn(),
    updateTicketStatus: jest.fn(),
    updateTicket: jest.fn(),
    assignTicket: jest.fn(),
    ccTicket: jest.fn(),
    deleteTicket: jest.fn(),
    getTicketHistory: jest.fn(),
  },
}));

jest.mock('@/lib/api/ticket-approval-api', () => ({
  TicketApprovalApi: { getApprovalDecisions: jest.fn() },
}));

jest.mock('@/lib/api/ticket-relations-api', () => ({
  TicketRelationsApi: { getRelationStats: jest.fn() },
}));

jest.mock('@/lib/api/user-api', () => ({
  UserApi: { getUsers: jest.fn() },
}));

jest.mock('@/lib/store/auth-store', () => {
  const hasPermission = jest.fn(() => false);
  return {
    useAuthStore: jest.fn((selector: (s: unknown) => unknown) =>
      selector({ user: undefined, hasPermission })
    ),
  };
});

jest.mock('@/lib/hooks/useErrorHandler', () => {
  const handleError = jest.fn();
  return { useErrorHandler: () => ({ handleError }) };
});

jest.mock('@/components/business/AISuggestionPanel', () => ({
  AISuggestionPanel: () => null,
}));

jest.mock('@/components/business/detail-tabs', () => ({
  CommentPanel: () => null,
  AttachmentPanel: () => null,
  HistoryTimeline: () => null,
  ApprovalWorkflowPanel: () => null,
  ticketCommentAdapter: { list: jest.fn() },
  ticketAttachmentAdapter: { list: jest.fn() },
  fetchAuditLogHistory: jest.fn(),
}));

jest.mock('@/components/ticket-relations/RelationPanel', () => ({
  RelationPanel: () => null,
}));

jest.mock('../ServiceRequestPanel', () => () => null);
jest.mock('../ServiceCatalogApprovalChain', () => () => null);
jest.mock('../CIContextCard', () => ({ CIContextCard: () => null }));
jest.mock('../KBRecommendCard', () => ({ KBRecommendCard: () => null }));

import { TicketApi } from '@/lib/api/ticket-api';
import { TicketApprovalApi } from '@/lib/api/ticket-approval-api';
import { TicketRelationsApi } from '@/lib/api/ticket-relations-api';
import { UserApi } from '@/lib/api/user-api';

const mockGetTicket = TicketApi.getTicket as jest.Mock;
const mockGetSLA = TicketApi.getTicketSLA as jest.Mock;
const mockGetUsers = UserApi.getUsers as jest.Mock;
const mockGetDecisions = TicketApprovalApi.getApprovalDecisions as jest.Mock;
const mockGetRelationStats = TicketRelationsApi.getRelationStats as jest.Mock;
const mockGetHistory = TicketApi.getTicketHistory as jest.Mock;

const baseTicket = {
  id: 101,
  ticketNumber: 'TKT-20260826-001',
  title: 'VPN 无法连接',
  description: '办公网 VPN 无法连接，需要协助排查。',
  priority: 'high',
  source: 'web',
  createdAt: '2026-08-26T08:00:00Z',
  updatedAt: '2026-08-26T09:00:00Z',
  version: 1,
  actions: {
    approve: { allowed: true },
    reject: { allowed: true },
    assign: { allowed: true },
    edit: { allowed: true },
    cc: { allowed: true },
    delete: { allowed: true },
  },
};

describe('TicketDetail', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetSLA.mockResolvedValue(null);
    mockGetUsers.mockResolvedValue({ users: [] });
    mockGetDecisions.mockResolvedValue([]);
    mockGetRelationStats.mockResolvedValue({ totalRelations: 0 });
    mockGetHistory.mockResolvedValue([]);
  });

  it('renders Chinese label for open status instead of raw "open"', async () => {
    mockGetTicket.mockResolvedValueOnce({ ...baseTicket, status: 'open' });

    render(<TicketDetail />);

    await waitFor(() => {
      expect(screen.getAllByText('待处理').length).toBeGreaterThan(0);
    });
    expect(screen.queryByText('open')).not.toBeInTheDocument();
  });

  it('renders Chinese label for assigned status instead of raw "assigned"', async () => {
    mockGetTicket.mockResolvedValueOnce({ ...baseTicket, status: 'assigned' });

    render(<TicketDetail />);

    await waitFor(() => {
      expect(screen.getAllByText('已分配').length).toBeGreaterThan(0);
    });
    expect(screen.queryByText('assigned')).not.toBeInTheDocument();
  });
});

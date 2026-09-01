/**
 * TicketDetail Component Tests
 *
 * 覆盖本次工作台重构的回归面：
 * - open / assigned 等真实工单状态必须渲染中文文案（不能回退成英文原文）
 */

import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent, { PointerEventsCheckLevel } from '@testing-library/user-event';
import TicketDetail from '../TicketDetail';

const mockHasPermission: jest.Mock<boolean, [string]> = jest.fn((_permission: string) => false);

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

jest.mock('@/lib/api/bpmn-workflow-api', () => ({
  BPMNWorkflowApi: { getTicketApprovalDecisions: jest.fn() },
}));

jest.mock('@/lib/api/ticket-relations-api', () => ({
  TicketRelationsApi: { getRelationStats: jest.fn() },
}));

jest.mock('@/lib/api/user-api', () => ({
  UserApi: { getUsers: jest.fn() },
}));

jest.mock('@/lib/api/ticket-notification-api', () => ({
  TicketNotificationApi: {
    getTicketNotifications: jest.fn(),
    sendTicketNotification: jest.fn(),
    markTicketNotificationRead: jest.fn(),
  },
}));

jest.mock('@/lib/store/auth-store', () => {
  return {
    useAuthStore: jest.fn((selector: (s: unknown) => unknown) =>
      selector({ user: { id: 7 }, hasPermission: mockHasPermission })
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
jest.mock('@/components/common/UserSelect', () => ({ UserSelect: () => null }));

import { TicketApi } from '@/lib/api/ticket-api';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
import { TicketRelationsApi } from '@/lib/api/ticket-relations-api';
import { UserApi } from '@/lib/api/user-api';
import { TicketNotificationApi } from '@/lib/api/ticket-notification-api';

const mockGetTicket = TicketApi.getTicket as jest.Mock;
const mockGetSLA = TicketApi.getTicketSLA as jest.Mock;
const mockGetUsers = UserApi.getUsers as jest.Mock;
const mockGetDecisions = BPMNWorkflowApi.getTicketApprovalDecisions as jest.Mock;
const mockGetRelationStats = TicketRelationsApi.getRelationStats as jest.Mock;
const mockGetHistory = TicketApi.getTicketHistory as jest.Mock;
const mockGetTicketNotifications = TicketNotificationApi.getTicketNotifications as jest.Mock;

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
    mockHasPermission.mockImplementation(() => false);
    mockGetSLA.mockResolvedValue(null);
    mockGetUsers.mockResolvedValue({ users: [] });
    mockGetDecisions.mockResolvedValue([]);
    mockGetRelationStats.mockResolvedValue({ totalRelations: 0 });
    mockGetHistory.mockResolvedValue([]);
    mockGetTicketNotifications.mockResolvedValue({ notifications: [], total: 0 });
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

  it('does not expose legacy ticket approval controls even if stale action flags are present', async () => {
    mockGetTicket.mockResolvedValueOnce({ ...baseTicket, status: 'pending_approval' });

    const { container } = render(<TicketDetail />);

    await screen.findByText('工单诉求与业务描述');
    const buttonText = Array.from(container.querySelectorAll('button')).map(button => button.textContent);
    expect(buttonText).not.toContain('批准');
    expect(buttonText).not.toContain('拒绝');
  });

  it('mounts the notification section lazily for users with notification:read', async () => {
    mockHasPermission.mockImplementation(permission => permission === 'notification:read');
    mockGetTicket.mockResolvedValueOnce({ ...baseTicket, status: 'open' });
    const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });

    render(<TicketDetail />);

    const notificationTab = await screen.findByText('工单通知');
    expect(mockGetTicketNotifications).not.toHaveBeenCalled();

    await user.click(notificationTab);

    expect(await screen.findByText('通知历史')).toBeInTheDocument();
    expect(mockGetTicketNotifications).toHaveBeenCalledWith(101);
    expect(screen.queryByText('发送通知')).not.toBeInTheDocument();
  });

  it('hides the notification tab without notification:read even when create is granted', async () => {
    mockHasPermission.mockImplementation(permission => permission === 'notification:create');
    mockGetTicket.mockResolvedValueOnce({ ...baseTicket, status: 'open' });

    render(<TicketDetail />);

    await screen.findByText('工单诉求与业务描述');
    expect(screen.queryByText('工单通知')).not.toBeInTheDocument();
    expect(mockGetTicketNotifications).not.toHaveBeenCalled();
  });

  it('shows notification send controls only with notification:create', async () => {
    mockHasPermission.mockImplementation(permission =>
      permission === 'notification:read' || permission === 'notification:create'
    );
    mockGetTicket.mockResolvedValueOnce({ ...baseTicket, status: 'open' });
    const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });

    render(<TicketDetail />);
    await user.click(await screen.findByText('工单通知'));

    expect(await screen.findByText('发送通知')).toBeInTheDocument();
  });
});

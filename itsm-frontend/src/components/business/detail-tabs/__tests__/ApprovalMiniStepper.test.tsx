/**
 * ApprovalMiniStepper Component Tests
 *
 * 覆盖：
 * - 有审批决策时渲染 ✓/●/○ 时间轴（复用 toApprovalSteps 单一事实源）
 * - 无审批决策时展示空态文案
 */

import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import { ApprovalMiniStepper } from '../ApprovalMiniStepper';

jest.mock('@/lib/api/ticket-approval-api', () => ({
  TicketApprovalApi: { getApprovalDecisions: jest.fn() },
}));

import { TicketApprovalApi } from '@/lib/api/ticket-approval-api';

const mockGetDecisions = TicketApprovalApi.getApprovalDecisions as jest.Mock;

describe('ApprovalMiniStepper', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders compact timeline when approval decisions exist', async () => {
    mockGetDecisions.mockResolvedValueOnce([
      {
        id: 1,
        nodeKey: '主管审批',
        decision: 'approved',
        actorId: 10,
        actorName: '王主管',
        comment: '同意',
        createdAt: '2026-08-26T08:00:00Z',
      },
      {
        id: 2,
        nodeKey: '自动化交付',
        decision: 'rejected',
        actorId: 11,
        actorName: '系统',
        comment: '',
        createdAt: '2026-08-26T09:00:00Z',
      },
    ]);

    render(<ApprovalMiniStepper ticketId={101} />);

    await waitFor(() => {
      expect(screen.getByText('主管审批')).toBeInTheDocument();
    });
    expect(screen.getByText('自动化交付')).toBeInTheDocument();
    expect(screen.getByText('(王主管)')).toBeInTheDocument();
    expect(screen.getByText('(系统)')).toBeInTheDocument();
    expect(screen.getByText('已通过')).toBeInTheDocument();
    expect(screen.getByText('已拒绝')).toBeInTheDocument();
  });

  it('shows empty state when there are no approval decisions', async () => {
    mockGetDecisions.mockResolvedValueOnce([]);

    render(<ApprovalMiniStepper ticketId={202} />);

    await waitFor(() => {
      expect(screen.getByText('该工单未走审批流程')).toBeInTheDocument();
    });
  });
});

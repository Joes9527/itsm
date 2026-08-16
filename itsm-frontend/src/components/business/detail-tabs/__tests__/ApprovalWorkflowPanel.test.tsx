import React from 'react';
import { render, screen, waitFor } from '@/lib/test-utils';

const mockGetApprovalDecisions = jest.fn();

jest.mock('@/lib/api/ticket-approval-api', () => ({
  TicketApprovalApi: {
    getApprovalDecisions: (...args: unknown[]) => mockGetApprovalDecisions(...args),
  },
}));

import ApprovalWorkflowPanel from '../ApprovalWorkflowPanel';

describe('ApprovalWorkflowPanel — 真实审批决策展示', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('接口返回真实决策记录时，渲染审批时间线而不是"未走审批流程"', async () => {
    mockGetApprovalDecisions.mockResolvedValue([
      {
        id: 1,
        processInstanceId: 10,
        processInstanceKey: 'PI-ticket_general_flow-1',
        processTaskId: 100,
        taskId: 'TASK-1',
        processDefinitionKey: 'ticket_general_flow',
        nodeKey: 'Activity_Approve',
        businessType: 'ticket',
        businessId: '5',
        actorId: 7,
        actorName: '张三',
        action: 'approve',
        decision: 'approved',
        comment: '同意',
        createdAt: '2026-08-16T10:00:00Z',
      },
    ]);

    render(
      <ApprovalWorkflowPanel
        ticketId={5}
        isTicketFinal={false}
      />
    );

    await waitFor(() => expect(mockGetApprovalDecisions).toHaveBeenCalledWith(5));
    // 审批人姓名与"审批人："前缀渲染在同一个 <Text> 节点内的相邻文本节点里，
    // 精确匹配会因为节点自身文本是"审批人：张三"而找不到单独的"张三"，需要子串匹配。
    expect(await screen.findByText('张三', { exact: false })).toBeInTheDocument();
    expect(screen.queryByText('该工单未走审批流程')).not.toBeInTheDocument();
  });

  it('接口返回空数组时，展示"未走审批流程"（真实的空，不是吞错误后的假空）', async () => {
    mockGetApprovalDecisions.mockResolvedValue([]);

    render(
      <ApprovalWorkflowPanel
        ticketId={6}
        isTicketFinal={false}
      />
    );

    await waitFor(() => expect(mockGetApprovalDecisions).toHaveBeenCalledWith(6));
    expect(await screen.findByText('该工单未走审批流程')).toBeInTheDocument();
  });
});

/**
 * ApprovalMiniStepper Component Tests
 *
 * 覆盖：
 * - 有审批决策时渲染 ✓/●/○ 时间轴（复用 toApprovalSteps 单一事实源）
 * - 无审批决策时展示空态文案
 */

import React from 'react';
import { ApprovalDecisionHistoryProvider } from '../ApprovalDecisionHistoryContext';
import { render, screen, waitFor } from '@testing-library/react';
import { ApprovalMiniStepper } from '../ApprovalMiniStepper';

jest.mock('@/lib/api/bpmn-workflow-api', () => ({
  BPMNWorkflowApi: { getTicketApprovalDecisions: jest.fn() },
}));

import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
import { WorkItemProcessTasksProvider, usePublishWorkItemProcessTasks } from '../WorkItemProcessTasksContext';

const mockGetDecisions = BPMNWorkflowApi.getTicketApprovalDecisions as jest.Mock;

/** 扮演 TicketProcessTasks：发布它那一次已确认的流程任务读取结果。 */
function TaskPublisher({ tasks }: { tasks: any[] | undefined }) {
  usePublishWorkItemProcessTasks('generic:202', tasks);
  return null;
}
const task = (over: Record<string, unknown> = {}) => ({
  id: 1, taskName: '任务分配', taskPurpose: '', status: 'created',
  assignmentState: 'unassigned', businessType: 'generic', businessId: 202, ...over,
});
const show = (tasks?: any[]) => render(
  <WorkItemProcessTasksProvider>
    <TaskPublisher tasks={tasks} />
    <ApprovalDecisionHistoryProvider ticketId={202}><ApprovalMiniStepper ticketId={202} /></ApprovalDecisionHistoryProvider>
  </WorkItemProcessTasksProvider>
);

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

    render(<ApprovalDecisionHistoryProvider ticketId={101}><ApprovalMiniStepper ticketId={101} /></ApprovalDecisionHistoryProvider>);

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

    render(<ApprovalDecisionHistoryProvider ticketId={202}><ApprovalMiniStepper ticketId={202} /></ApprovalDecisionHistoryProvider>);

    await waitFor(() => {
      expect(screen.getByText('暂无审批决策记录')).toBeInTheDocument();
    });
  });

  // TKT-202609-000070 的现象：流程停在派单节点，审批决策表里一条记录都没有。
  // 过去空态只有「暂无审批决策记录」，用户无法判断该等派单还是该报障。
  it('explains that the flow is stuck before the approval node', async () => {
    mockGetDecisions.mockResolvedValueOnce([]);
    show([task()]);

    expect(await screen.findByText(/流程当前停在「任务分配」（等待分配处理人）/)).toBeInTheDocument();
  });

  it('explains that an approval node exists but is unprocessed', async () => {
    mockGetDecisions.mockResolvedValueOnce([]);
    show([
      task({ id: 1, taskName: '任务分配', status: 'completed' }),
      task({ id: 2, taskName: '工单审批', taskPurpose: 'approval', status: 'created', assignmentState: 'assigned' }),
    ]);

    expect(await screen.findByText(/流程已到达审批节点「工单审批」，尚未处理/)).toBeInTheDocument();
  });

  // 审批节点已经走完却没有决策记录是数据缺陷，必须与"还没审批"区分开。
  it('reports finished approval nodes without decisions as a defect', async () => {
    mockGetDecisions.mockResolvedValueOnce([]);
    show([task({ id: 2, taskName: '工单审批', taskPurpose: 'approval', status: 'completed', assignmentState: 'terminal' })]);

    expect(await screen.findByText(/已全部结束，但没有对应的审批决策记录/)).toBeInTheDocument();
  });

  it('explains that the flow never reaches an approval node', async () => {
    mockGetDecisions.mockResolvedValueOnce([]);
    show([task({ id: 3, taskName: '通知请求人', status: 'completed', assignmentState: 'terminal' })]);

    expect(await screen.findByText(/未经过审批节点/)).toBeInTheDocument();
  });

  // 流程任务读不出来时不编造原因，退回原有的空态文案。
  it('omits the reason when no confirmed task read is published', async () => {
    mockGetDecisions.mockResolvedValueOnce([]);
    show(undefined);

    await waitFor(() => expect(screen.getByText('暂无审批决策记录')).toBeInTheDocument());
    expect(screen.queryByText(/流程当前停在/)).not.toBeInTheDocument();
  });
});

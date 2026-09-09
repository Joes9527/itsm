import React from 'react';
import { act, render, screen, waitFor } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import { ChangeApi, type Change } from '@/lib/api/change-api';
import ChangeDetail from '../ChangeDetail';
import { ChangeActions } from '../ChangeActions';

jest.mock('next/navigation', () => ({
  useParams: () => ({ id: '1' }),
  useRouter: () => ({ push: jest.fn() }),
}));
jest.mock('@/lib/api/change-api', () => ({
  ...jest.requireActual('@/lib/api/change-api'),
  ChangeApi: {
    getChange: jest.fn(),
    getChangeApprovals: jest.fn(),
    executeAction: jest.fn(),
    getTaskProgress: jest.fn(),
  },
}));
jest.mock('../ChangeCMDBImpactPanel', () => () => null);
jest.mock('../ChangeRiskAssessment', () => () => null);
jest.mock('../ChangeRollbackPlan', () => () => null);
const change = {
  id: 1,
  workItemId: 31,
  number: 'CHG-2026-31',
  version: 7,
  title: '生产变更',
  description: 'details',
  justification: 'reason',
  type: 'normal',
  status: 'pending',
  priority: 'high',
  riskLevel: 'low',
  impactScope: 'low',
  outcome: '',
  outcomeEvidence: '',
  reviewEvidence: '',
  reviewedBy: 0,
  reviewedAt: null,
  standardTemplateId: 0,
  createdBy: 1,
  createdByName: 'Operator',
  tenantId: 1,
  createdAt: '',
  updatedAt: '',
  implementationPlan: 'deploy',
  rollbackPlan: 'restore',
  affectedCis: [],
  relatedTickets: [],
  actions: { assess: { allowed: true }, approve: { allowed: false, reason: '等待评估' } },
  currentTasks: { assess: 'real-assessment' },
} satisfies Change;
const receipt = { workItemId: 31, version: 8, status: 'pending', replayed: false };
beforeEach(() => {
  jest.clearAllMocks();
  Object.defineProperty(global.crypto, 'randomUUID', {
    configurable: true,
    value: jest.fn(() => 'stable-operation'),
  });
  jest.mocked(ChangeApi.getChange).mockResolvedValue(change);
  jest.mocked(ChangeApi.getChangeApprovals).mockResolvedValue([]);
});
async function assess() {
  const user = userEvent.setup();
  await user.click(await screen.findByRole('button', { name: '完成评估' }));
  await user.type(screen.getByLabelText('操作依据'), 'checked risk');
  await user.click(screen.getByRole('button', { name: /^提\s*交$/ }));
  return user;
}
test('server action/task authority and actual number are used', async () => {
  jest
    .mocked(ChangeApi.executeAction)
    .mockResolvedValue({
      progress: 'completed',
      taskId: 'real-assessment',
      executionKey: 'callback',
      result: receipt,
    });
  render(<ChangeDetail id='1' />);
  await assess();
  expect(ChangeApi.executeAction).toHaveBeenCalledWith(1, 'assess', {
    expectedVersion: 7,
    operationId: 'stable-operation',
    taskId: 'real-assessment',
    evidence: 'checked risk',
  });
  expect(await screen.findByText('当前任务回调已完成')).toBeInTheDocument();
  expect(screen.getByText('CHG-2026-31')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: /^批\s*准$/ })).toBeDisabled();
});
test('accepted work is inspected, not resubmitted, and durable blocked effect remains visible', async () => {
  jest
    .mocked(ChangeApi.executeAction)
    .mockResolvedValue({
      progress: 'pending',
      taskId: 'real-assessment',
      executionKey: 'callback',
    });
  jest
    .mocked(ChangeApi.getTaskProgress)
    .mockResolvedValue({
      progress: 'blocked',
      taskId: 'real-assessment',
      executionKey: 'callback',
      reason: 'handler_contract',
      result: receipt,
    });
  render(<ChangeDetail id='1' />);
  const user = await assess();
  expect(await screen.findByText('流程推进等待中')).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: '查询进度' }));
  expect(await screen.findByText('流程推进受阻，需要人工处理')).toBeInTheDocument();
  expect(screen.getByText('业务结果已提交：版本 8，状态 pending')).toBeInTheDocument();
  expect(ChangeApi.executeAction).toHaveBeenCalledTimes(1);
  expect(ChangeApi.getTaskProgress).toHaveBeenCalledWith(1, 'stable-operation', 'assess');
});
test('uncertain identical retry keeps original operation and observed version', async () => {
  jest
    .mocked(ChangeApi.executeAction)
    .mockRejectedValueOnce(new Error('network lost'))
    .mockResolvedValueOnce({
      progress: 'effect_applied',
      taskId: 'real-assessment',
      executionKey: 'callback',
      reason: 'continuation_progress_unavailable',
      result: receipt,
    });
  render(<ChangeDetail id='1' />);
  const user = await assess();
  expect(await screen.findByText('network lost')).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: /^提\s*交$/ }));
  expect(await screen.findByText('业务结果已提交，后续流程进度不可用')).toBeInTheDocument();
  expect(jest.mocked(ChangeApi.executeAction).mock.calls[0]).toEqual(
    jest.mocked(ChangeApi.executeAction).mock.calls[1]
  );
});
test('closed failed outcome remains failure, independent of closure', async () => {
  jest
    .mocked(ChangeApi.getChange)
    .mockResolvedValue({
      ...change,
      status: 'completed',
      outcome: 'failed',
      outcomeEvidence: 'deployment failed',
      actions: {},
      currentTasks: {},
    });
  render(<ChangeDetail id='1' />);
  expect(await screen.findByText('deployment failed')).toBeInTheDocument();
  expect(screen.getByText('failed')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '开始实施' })).not.toBeInTheDocument();
});

test('accepted work stops automatic inspection after five reads', async () => {
  jest.useFakeTimers();
  try {
    const user = userEvent.setup({ advanceTimers: jest.advanceTimersByTime });
    const pending = {
      progress: 'pending' as const,
      taskId: 'real-assessment',
      executionKey: 'callback',
    };
    jest.mocked(ChangeApi.executeAction).mockResolvedValue(pending);
    jest.mocked(ChangeApi.getTaskProgress).mockResolvedValue(pending);
    render(<ChangeActions change={change} onRefresh={async () => {}} />);
    await user.click(screen.getByRole('button', { name: '完成评估' }));
    await user.type(screen.getByLabelText('操作依据'), 'checked risk');
    await user.click(screen.getByRole('button', { name: /^提\s*交$/ }));
    for (let i = 0; i < 8; i++)
      await act(async () => {
        jest.advanceTimersByTime(2000);
      });
    expect(ChangeApi.getTaskProgress).toHaveBeenCalledTimes(5);
    expect(ChangeApi.executeAction).toHaveBeenCalledTimes(1);
    expect(screen.getByText('自动查询已暂停，可手动查询进度。')).toBeInTheDocument();
  } finally {
    jest.useRealTimers();
  }
});

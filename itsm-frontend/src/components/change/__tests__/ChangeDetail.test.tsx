/**
 * ChangeDetail Component Tests
 *
 * 覆盖代码审查发现的契约问题：TransitionStatus 后端要求驳回必须填写意见，
 * 但前端曾经把这个字段标注成"可选"且吞掉了真实的后端错误信息。
 */

import React from 'react';
import { render, screen, waitFor, within } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import { message } from 'antd';
import { WorkItemProvider } from '@/components/work-item/WorkItemContext';
import type { WorkItemActionState, WorkItemCommon } from '@/components/work-item/WorkItemTypes';

jest.mock('next/navigation', () => ({
  useParams: () => ({ id: '1' }),
  useRouter: () => ({ push: jest.fn(), back: jest.fn() }),
}));

const mockGetChange = jest.fn();
const mockGetApprovalSummary = jest.fn();
const mockApproveChange = jest.fn();
const mockRejectChange = jest.fn();
const mockSubmitForApproval = jest.fn();
const mockStartImplementation = jest.fn();
const mockCompleteImplementation = jest.fn();

jest.mock('@/lib/api/', () => ({
  ChangeApi: {
    getChange: (...args: unknown[]) => mockGetChange(...args),
    getApprovalSummary: (...args: unknown[]) => mockGetApprovalSummary(...args),
    approveChange: (...args: unknown[]) => mockApproveChange(...args),
    rejectChange: (...args: unknown[]) => mockRejectChange(...args),
    submitForApproval: (...args: unknown[]) => mockSubmitForApproval(...args),
    startImplementation: (...args: unknown[]) => mockStartImplementation(...args),
    completeImplementation: (...args: unknown[]) => mockCompleteImplementation(...args),
  },
}));

// antd 的 message 是挂载到 document.body 之外、带自动消失动画的静态 API，在 jsdom 里
// 断言它渲染出的文本容易受时序/动画影响而不稳定——直接断言调用参数，比断言瞬时 toast
// 的 DOM 文本更贴近我们真正关心的契约（提示了什么内容，而不是提示组件怎么渲染的）。
jest.mock('antd', () => ({
  ...jest.requireActual('antd'),
  message: {
    success: jest.fn(),
    error: jest.fn(),
    warning: jest.fn(),
  },
}));

jest.mock('dayjs', () => {
  const mockDate = {
    format: jest.fn(() => '2024-01-01 12:00'),
    isValid: () => true,
  };
  const mockDayjs: any = jest.fn(() => mockDate);
  mockDayjs.extend = jest.fn();
  mockDayjs.locale = jest.fn();
  return mockDayjs;
});

import ChangeDetail from '../ChangeDetail';
import {
  ChangeType,
  ChangePriority,
  ChangeStatus,
  ChangeImpact,
  ChangeRisk,
} from '@/constants/change';
import type { Change } from '@/lib/api/change-api';

const workItem: WorkItemCommon = {
  id: 101,
  number: 'C-101',
  recordClass: 'change_request',
  title: '升级生产数据库',
  status: ChangeStatus.PENDING,
  priority: ChangePriority.HIGH,
  requesterId: 1,
  createdAt: '2024-01-01T10:00:00Z',
  updatedAt: '2024-01-01T10:00:00Z',
};

const pendingChange = {
  id: 1,
  title: '升级生产数据库',
  description: '升级到新版本',
  justification: '安全补丁',
  type: ChangeType.NORMAL,
  status: ChangeStatus.PENDING,
  priority: ChangePriority.HIGH,
  impactScope: ChangeImpact.HIGH,
  riskLevel: ChangeRisk.MEDIUM,
  createdBy: 1,
  createdByName: 'Admin',
  tenantId: 1,
  implementationPlan: '计划',
  rollbackPlan: '回滚计划',
  affectedCis: [],
  relatedTickets: [],
  createdAt: '2024-01-01T10:00:00Z',
  updatedAt: '2024-01-01T10:00:00Z',
};

const allowedPendingActions = {
  submitForApproval: { allowed: false, reason: '只有草稿状态的变更可以提交审批' },
  approve: { allowed: true },
  reject: { allowed: true },
  startImplementation: { allowed: false, reason: '当前状态和变更类型不允许开始实施' },
  completeImplementation: { allowed: false, reason: '只有实施中的变更可以标记完成' },
} satisfies Record<string, WorkItemActionState>;

function renderWithWorkItemContext(
  actions: Record<string, WorkItemActionState>,
  fallbackActions?: Record<string, WorkItemActionState>
) {
  return render(
    <WorkItemProvider value={{ workItem, actions, onActionDispatch: jest.fn() }}>
      <ChangeDetail id='1' fallbackActions={fallbackActions} />
    </WorkItemProvider>
  );
}

function renderWithoutProvider(fallbackActions?: Record<string, WorkItemActionState>) {
  return render(<ChangeDetail id='1' fallbackActions={fallbackActions} />);
}

function renderWithRefreshingProvider(initialActions: Record<string, WorkItemActionState>) {
  function Harness() {
    const [summaryChange, setSummaryChange] = React.useState<Change>({
      ...pendingChange,
      actions: initialActions,
      workItemId: workItem.id,
    });

    return (
      <WorkItemProvider
        value={{
          workItem: { ...workItem, status: summaryChange.status },
          actions: summaryChange.actions ?? {},
          onActionDispatch: jest.fn(),
        }}
      >
        <ChangeDetail
          id='1'
          fallbackActions={summaryChange.actions}
          onChangeLoaded={setSummaryChange}
        />
      </WorkItemProvider>
    );
  }

  return render(<Harness />);
}

async function expectDisabledAction(label: string, reason: string) {
  const reasonNode = await screen.findByText(reason);
  const actionGroup = reasonNode.closest('.ant-space');

  expect(actionGroup).not.toBeNull();

  const button = within(actionGroup as HTMLElement).getByRole('button');
  expect(button.textContent?.replace(/\s+/g, '')).toBe(label.replace(/\s+/g, ''));
  expect(button).toBeDisabled();
  expect(button).toHaveAccessibleDescription(reason);
}

describe('ChangeDetail — 驳回意见契约', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetChange.mockResolvedValue(pendingChange);
    mockGetApprovalSummary.mockResolvedValue([]);
  });

  it('拒绝原因输入框标注为必填，不再显示"可选"', async () => {
    renderWithoutProvider(allowedPendingActions);
    await waitFor(() => expect(mockGetChange).toHaveBeenCalled());

    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '拒绝' }));

    expect(screen.getByText(/拒绝原因/)).toBeInTheDocument();
    expect(screen.queryByText(/拒绝原因（可选）/)).not.toBeInTheDocument();
  });

  it('不填写拒绝原因时，点击拒绝不会调用后端，且提示需要填写', async () => {
    renderWithoutProvider(allowedPendingActions);
    await waitFor(() => expect(mockGetChange).toHaveBeenCalled());

    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '拒绝' }));

    // 页面上和弹窗里各有一个文案是"拒绝"的按钮，用弹窗的可访问名（标题）把查询范围
    // 限定在弹窗内部，避免误点到弹窗外面那个用来打开弹窗的触发按钮。
    const dialog = await screen.findByRole('dialog', { name: '拒绝变更' });
    await user.click(within(dialog).getByRole('button', { name: /拒\s*绝/ }));

    await waitFor(() => expect(message.warning).toHaveBeenCalledWith('请填写拒绝原因'));
    expect(mockRejectChange).not.toHaveBeenCalled();
    // 校验没通过应该让弹窗留在原地，不应该被当成"取消"关掉。
    expect(screen.getByPlaceholderText('请输入拒绝原因...')).toBeInTheDocument();
  });

  it('填写拒绝原因后提交失败时，展示后端返回的真实错误信息而不是通用文案', async () => {
    mockRejectChange.mockRejectedValue(new Error('驳回变更时必须填写意见'));

    renderWithoutProvider(allowedPendingActions);
    await waitFor(() => expect(mockGetChange).toHaveBeenCalled());

    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '拒绝' }));

    const dialog = await screen.findByRole('dialog', { name: '拒绝变更' });
    const textarea = within(dialog).getByPlaceholderText('请输入拒绝原因...');
    await user.type(textarea, '风险太高');
    await user.click(within(dialog).getByRole('button', { name: /拒\s*绝/ }));

    await waitFor(() => expect(mockRejectChange).toHaveBeenCalledWith(1, { comment: '风险太高' }));
    await waitFor(() => expect(message.error).toHaveBeenCalledWith('驳回变更时必须填写意见'));
    expect(message.error).not.toHaveBeenCalledWith('拒绝失败');
  });
});

describe('ChangeDetail action eligibility', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetChange.mockResolvedValue(pendingChange);
    mockGetApprovalSummary.mockResolvedValue([]);
    mockApproveChange.mockResolvedValue({ ...pendingChange, status: ChangeStatus.APPROVED });
    mockRejectChange.mockResolvedValue({ ...pendingChange, status: ChangeStatus.REJECTED });
    mockSubmitForApproval.mockResolvedValue({ ...pendingChange, status: ChangeStatus.PENDING });
    mockStartImplementation.mockResolvedValue({
      ...pendingChange,
      status: ChangeStatus.IN_PROGRESS,
    });
    mockCompleteImplementation.mockResolvedValue({
      ...pendingChange,
      status: ChangeStatus.COMPLETED,
    });
  });

  it('prefers provider actions over fallback actions and exposes denied reasons', async () => {
    renderWithWorkItemContext(
      { startImplementation: { allowed: false, reason: '上下文动作优先' } },
      { startImplementation: { allowed: true } }
    );

    await expectDisabledAction('开始实施', '上下文动作优先');
  });

  it('uses fallback actions without a WorkItemProvider', async () => {
    renderWithoutProvider({
      completeImplementation: { allowed: false, reason: '历史变更未回填 WorkItem 动作' },
    });

    await expectDisabledAction('完成', '历史变更未回填 WorkItem 动作');
    expect(mockGetChange).toHaveBeenCalledWith(1);
  });

  it('uses distinct backend approval and rejection states', async () => {
    renderWithWorkItemContext({
      approve: { allowed: false, reason: '不能审批自己提交的变更' },
      reject: { allowed: false, reason: '不能驳回自己提交的变更' },
    });

    await expectDisabledAction('批准', '不能审批自己提交的变更');
    await expectDisabledAction('拒绝', '不能驳回自己提交的变更');
  });

  it('does not expose start implementation for a normal approved change when backend denies it', async () => {
    mockGetChange.mockResolvedValue({
      ...pendingChange,
      type: ChangeType.NORMAL,
      status: ChangeStatus.APPROVED,
    });

    renderWithWorkItemContext({
      startImplementation: { allowed: false, reason: '当前状态和变更类型不允许开始实施' },
    });

    await expectDisabledAction('开始实施', '当前状态和变更类型不允许开始实施');
  });

  it('fails closed when the backend omits change actions', async () => {
    renderWithoutProvider();

    await screen.findByText('升级生产数据库');
    expect(screen.queryByRole('button', { name: '提交审批' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '批准' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '拒绝' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '开始实施' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '完成' })).not.toBeInTheDocument();
  });

  it('refreshes provider actions after mutation detail reload', async () => {
    const approvedActions = {
      approve: { allowed: false, reason: '只有已提交待审批的变更可以批准' },
      reject: { allowed: false, reason: '只有已提交待审批的变更可以批准' },
      startImplementation: { allowed: false, reason: '当前状态和变更类型不允许开始实施' },
    } satisfies Record<string, WorkItemActionState>;

    mockGetChange
      .mockResolvedValueOnce({ ...pendingChange, actions: allowedPendingActions })
      .mockResolvedValueOnce({
        ...pendingChange,
        status: ChangeStatus.APPROVED,
        actions: approvedActions,
      });

    renderWithRefreshingProvider(allowedPendingActions);

    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '批准' }));
    const dialog = await screen.findByRole('dialog', { name: '批准变更' });
    await user.click(within(dialog).getByRole('button', { name: /批\s*准/ }));

    await waitFor(() => expect(mockApproveChange).toHaveBeenCalledWith(1, { comment: '' }));
    await expectDisabledAction('开始实施', '当前状态和变更类型不允许开始实施');
  });
});

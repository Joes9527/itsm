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

jest.mock('next/navigation', () => ({
  useParams: () => ({ id: '1' }),
  useRouter: () => ({ push: jest.fn(), back: jest.fn() }),
}));

const mockGetChange = jest.fn();
const mockGetApprovalSummary = jest.fn();
const mockApproveChange = jest.fn();
const mockRejectChange = jest.fn();

jest.mock('@/lib/api/', () => ({
  ChangeApi: {
    getChange: (...args: unknown[]) => mockGetChange(...args),
    getApprovalSummary: (...args: unknown[]) => mockGetApprovalSummary(...args),
    approveChange: (...args: unknown[]) => mockApproveChange(...args),
    rejectChange: (...args: unknown[]) => mockRejectChange(...args),
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
import { ChangeType, ChangePriority, ChangeStatus } from '@/constants/change';

const pendingChange = {
  id: 1,
  title: '升级生产数据库',
  description: '升级到新版本',
  justification: '安全补丁',
  type: ChangeType.NORMAL,
  status: ChangeStatus.PENDING,
  priority: ChangePriority.HIGH,
  impactScope: 'high',
  riskLevel: 'medium',
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

describe('ChangeDetail — 驳回意见契约', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetChange.mockResolvedValue(pendingChange);
    mockGetApprovalSummary.mockResolvedValue([]);
  });

  it('拒绝原因输入框标注为必填，不再显示"可选"', async () => {
    render(<ChangeDetail />);
    await waitFor(() => expect(mockGetChange).toHaveBeenCalled());

    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '拒绝' }));

    expect(screen.getByText(/拒绝原因/)).toBeInTheDocument();
    expect(screen.queryByText(/拒绝原因（可选）/)).not.toBeInTheDocument();
  });

  it('不填写拒绝原因时，点击拒绝不会调用后端，且提示需要填写', async () => {
    render(<ChangeDetail />);
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

    render(<ChangeDetail />);
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

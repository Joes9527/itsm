/**
 * TicketDetail Component Tests
 *
 * 覆盖本次工作台重构的回归面：
 * - open / assigned 等真实工单状态必须渲染中文文案（不能回退成英文原文）
 */

import React from 'react';
import { render, screen, waitFor, fireEvent, within, act } from '@testing-library/react';
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
jest.mock('@/lib/api/service-catalog-api', () => ({
  ServiceCatalogApi: { getServiceRequestByTicketId: jest.fn(async () => ({id: 55, ciId: 88, formData: {_approval_chain: [{level: 1, name: 'Source regression approval', role: 'dept_manager', approval_type: 'serial'}]}})) },
}));
jest.mock('@/lib/api/cmdb-api', () => ({
  CMDBApi: {
    getCI: jest.fn(async () => ({id: 88, name: 'Request linked server'})),
    getCITopology: jest.fn(async () => ({totalNodes: 1, totalEdges: 0})),
  },
}));
jest.mock('../KBRecommendCard', () => ({ KBRecommendCard: () => null }));
jest.mock('@/components/common/UserSelect', () => ({ UserSelect: () => null }));

import { useErrorHandler } from '@/lib/hooks/useErrorHandler';
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
  it.each([
    ['kaf_web', 'service_request_item', 'KAF Web 申请', true],
    ['service_catalog', 'service_request_item', '服务目录申请', true],
    ['service_catalog', 'generic', '服务目录申请', false],
    ['kaf_web', 'incident', 'KAF Web 申请', false],
    ['service_catalog', 'change_request', '服务目录申请', false],
    ['service_catalog', 'problem', '服务目录申请', false],
    ['service_catalog', 'catalog_task', '服务目录申请', false],
  ])('uses source %s as a label and %s as approval ownership', async (source, recordClass, label, hasChain) => {
    mockGetTicket.mockResolvedValueOnce({...baseTicket, source, recordClass, status: 'new'});
    const user = userEvent.setup({pointerEventsCheck: PointerEventsCheckLevel.Never});
    render(<TicketDetail />);
    expect(await screen.findByText(label as string)).toBeInTheDocument();
    await user.click(await screen.findByText(/^审批链/));
    if (hasChain) {
      expect(await screen.findByText(/L1: Source regression approval/)).toBeInTheDocument();
      expect(await screen.findByText('Request linked server')).toBeInTheDocument();
    } else {
      expect(screen.queryByText(/L1: Source regression approval/)).not.toBeInTheDocument();
      expect(screen.queryByText('Request linked server')).not.toBeInTheDocument();
    }
  });

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
  it('does not present the base new status as KAF Requested Item progress', async () => {
    mockGetTicket.mockResolvedValueOnce({ ...baseTicket, source: 'kaf_web', recordClass: 'service_request_item', status: 'new' });
    render(<TicketDetail />);
    await screen.findByText('#101 VPN 无法连接');
    expect(screen.queryByText('新建')).not.toBeInTheDocument();
  });
  it('keeps a historical SLA breach visible during a fresh current cycle', async () => {
    mockGetTicket.mockResolvedValueOnce({ ...baseTicket, status: 'open' });
    mockGetSLA.mockResolvedValueOnce({
      slaName: '冻结 SLA', cycleNumber: 2, isBreached: false,
      responseTime: 60, resolutionTime: 60,
      responseDeadline: null, resolutionDeadline: null,
      responseTimeRemaining: 60, resolutionTimeRemaining: 60,
      history: [{ number: 1, responseBreached: false, resolutionBreached: true }],
    });
    render(<TicketDetail />);
    expect(await screen.findByText('当前周期 2')).toBeInTheDocument();
    expect(screen.getByText('历史周期 1：已违约')).toBeInTheDocument();
  });

  describe('edit command retries through the real form', () => {
    const update = TicketApi.updateTicket as jest.Mock;
    let originalRandomUUID: typeof crypto.randomUUID;
    beforeEach(() => {
      originalRandomUUID = crypto.randomUUID;
      let sequence = 0;
      Object.defineProperty(crypto, 'randomUUID', { configurable: true, value: jest.fn(() => `confirmed-edit-${++sequence}`) });
      update.mockReset();
      mockGetTicket.mockResolvedValue({ ...baseTicket, recordClass: 'generic', status: 'open' });
    });
    afterEach(() => Object.defineProperty(crypto, 'randomUUID', { configurable: true, value: originalRandomUUID }));

    it('submits generic new to in_progress to the backend with the opening version', async () => {
      mockGetTicket.mockResolvedValue({ ...baseTicket, recordClass: 'generic', status: 'new' });
      update.mockResolvedValue({ workItemId: 101, version: 2, status: 'in_progress' });
      const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
      render(<TicketDetail />);
      await user.click((await screen.findByText('编辑', { selector: 'span' })).closest('button')!);
      const dialog = await screen.findByRole('dialog');
      await user.click(within(dialog).getByLabelText('状态'));
      await user.click(await screen.findByText('处理中', { selector: '.ant-select-item-option-content' }));
      await user.click(within(dialog).getByText('保存修改').closest('button')!);
      await waitFor(() => expect(update).toHaveBeenCalledWith(101, expect.objectContaining({
        status: 'in_progress', version: 1, operationId: 'confirmed-edit-1',
      })));
    });

    it('surfaces backend lifecycle rejection and leaves the form available for correction', async () => {
      mockGetTicket.mockResolvedValue({ ...baseTicket, recordClass: 'generic', status: 'new' });
      const rejection = Object.assign(new Error('transition denied by backend'), { status: 400 });
      update.mockRejectedValue(rejection);
      const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
      render(<TicketDetail />);
      await user.click((await screen.findByText('编辑', { selector: 'span' })).closest('button')!);
      const dialog = await screen.findByRole('dialog');
      await user.click(within(dialog).getByLabelText('状态'));
      await user.click(await screen.findByText('处理中', { selector: '.ant-select-item-option-content' }));
      await user.click(within(dialog).getByText('保存修改').closest('button')!);
      await waitFor(() => expect(useErrorHandler().handleError).toHaveBeenCalledWith(rejection, 'updateTicket', '更新失败'));
      expect(within(dialog).getByText('保存修改')).toBeInTheDocument();
      expect(mockGetTicket).toHaveBeenCalledTimes(1);
    });

    it('requires a generic resolution and preserves the confirmed solution on an uncertain retry', async () => {
      mockGetTicket.mockResolvedValue({ ...baseTicket, recordClass: 'generic', status: 'in_progress' });
      update.mockRejectedValueOnce(new Error('response lost')).mockResolvedValueOnce({ workItemId: 101, version: 2, status: 'resolved', replayed: true });
      const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
      render(<TicketDetail />);
      await user.click((await screen.findByText('编辑', { selector: 'span' })).closest('button')!);
      const dialog = await screen.findByRole('dialog');
      await user.click(within(dialog).getByLabelText('状态'));
      await user.click(await screen.findByText('已解决', { selector: '.ant-select-item-option-content' }));
      await user.click(within(dialog).getByText('保存修改').closest('button')!);
      expect(await within(dialog).findByText('请填写解决方案')).toBeInTheDocument();
      expect(update).not.toHaveBeenCalled();
      await user.type(within(dialog).getByLabelText('解决方案'), '已恢复 VPN 配置并验证连接');
      await user.click(within(dialog).getByText('保存修改').closest('button')!);
      await waitFor(() => expect(update).toHaveBeenCalledTimes(1));
      const confirmed = update.mock.calls[0][1];
      expect(confirmed).toMatchObject({ status: 'resolved', resolution: '已恢复 VPN 配置并验证连接', version: 1, operationId: 'confirmed-edit-1' });
      await waitFor(() => expect(within(dialog).getByText('保存修改').closest('button')).not.toHaveClass('ant-btn-loading'));
      await user.click(within(dialog).getByText('保存修改').closest('button')!);
      await waitFor(() => expect(update).toHaveBeenCalledTimes(2));
      expect(update.mock.calls[1][1]).toEqual(confirmed);
    });

    it('loads the existing generic resolution for editing', async () => {
      mockGetTicket.mockResolvedValue({ ...baseTicket, recordClass: 'generic', status: 'in_progress', resolution: '已有解决记录' });
      const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
      render(<TicketDetail />);
      await user.click((await screen.findByText('编辑', { selector: 'span' })).closest('button')!);
      expect(within(await screen.findByRole('dialog')).getByLabelText('解决方案')).toHaveValue('已有解决记录');
    });

    it.each(['incident', 'problem', 'change_request', 'service_request_item', 'catalog_task'])('does not expose generic resolution editing for %s', async recordClass => {
      mockGetTicket.mockResolvedValue({ ...baseTicket, recordClass, status: 'in_progress' });
      const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
      render(<TicketDetail />);
      await user.click((await screen.findByText('编辑', { selector: 'span' })).closest('button')!);
      expect(within(await screen.findByRole('dialog')).queryByLabelText('解决方案')).not.toBeInTheDocument();
    });

    it('closes through a separate pure status command and preserves an uncertain retry', async () => {
      mockGetTicket.mockResolvedValue({ ...baseTicket, recordClass: 'generic', status: 'resolved', resolution: '保留解决方案',
        actions: { ...baseTicket.actions, edit: { allowed: false }, close: { allowed: true } } });
      update.mockRejectedValueOnce(new Error('response lost')).mockResolvedValueOnce({ workItemId: 101, version: 2, status: 'closed', replayed: true });
      const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
      render(<TicketDetail />);
      await user.click((await screen.findByText('关闭工单', { selector: 'span' })).closest('button')!);
      const dialog = await screen.findByRole('dialog');
      await user.click(within(dialog).getByRole('button', { name: '确认关闭' }));
      await waitFor(() => expect(update).toHaveBeenCalledTimes(1));
      expect(update.mock.calls[0]).toEqual([101, { status: 'closed', version: 1, operationId: 'confirmed-edit-1' }]);
      await waitFor(() => expect(within(dialog).getByRole('button', { name: '确认关闭' })).not.toHaveClass('ant-btn-loading'));
      mockGetTicket.mockResolvedValue({ ...baseTicket, recordClass: 'generic', status: 'closed', version: 2, actions: { ...baseTicket.actions, edit: { allowed: false }, close: { allowed: false } } });
      await user.click(within(dialog).getByRole('button', { name: '确认关闭' }));
      await waitFor(() => expect(update).toHaveBeenCalledTimes(2));
      expect(update.mock.calls[1]).toEqual(update.mock.calls[0]);
      await waitFor(() => expect(mockGetTicket).toHaveBeenCalledTimes(2));
      expect(screen.queryByText('关闭工单', { selector: 'button span' })).not.toBeInTheDocument();
    });

    it('discards close confirmation when the displayed ticket changes', async () => {
      mockGetTicket.mockImplementation(async (id: number) => ({ ...baseTicket, id, recordClass: 'generic', status: 'resolved', actions: { ...baseTicket.actions, close: { allowed: true } } }));
      const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
      const view = render(<TicketDetail id='101' />);
      await user.click((await screen.findByText('关闭工单', { selector: 'span' })).closest('button')!);
      view.rerender(<TicketDetail id='102' />);
      await screen.findByText('#102 VPN 无法连接');
      expect(screen.queryByText('确认关闭')).not.toBeInTheDocument();
      expect(update).not.toHaveBeenCalled();
    });

    it.each(['generic', 'incident'])('does not offer a close command without backend capability for %s', async recordClass => {
      mockGetTicket.mockResolvedValue({ ...baseTicket, recordClass, status: 'resolved', actions: { ...baseTicket.actions, close: { allowed: false } } });
      render(<TicketDetail />);
      await screen.findByText('工单诉求与业务描述');
      expect(screen.queryByText('关闭工单')).not.toBeInTheDocument();
    });

    it('keeps the form opening version across refresh and reuses the full uncertain request', async () => {
      update.mockRejectedValueOnce(new Error('connection lost after submission')).mockResolvedValueOnce({ workItemId: 101, version: 2, status: 'open', replayed: true });
      const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
      render(<TicketDetail />);
      await user.click((await screen.findByText('编辑', { selector: 'span' })).closest('button')!);
      let dialog = (await screen.findByText('编辑工单')).closest('[role="dialog"]')! as HTMLElement;
      await user.clear(within(dialog).getByLabelText('工单标题'));
      await user.type(within(dialog).getByLabelText('工单标题'), 'Confirmed title');
      mockGetTicket.mockResolvedValue({ ...baseTicket, recordClass: 'generic', status: 'open', version: 9 });
      fireEvent.keyDown(document.body, { key: 'r', altKey: true });
      await waitFor(() => expect(mockGetTicket).toHaveBeenCalledTimes(2));
      dialog = (await screen.findByText('编辑工单')).closest('[role="dialog"]')! as HTMLElement;
      await user.click(within(dialog).getByText('保存修改').closest('button')!);
      await waitFor(() => expect(update).toHaveBeenCalledTimes(1));
      const first = update.mock.calls[0][1];
      expect(first).toMatchObject({ title: 'Confirmed title', version: 1, operationId: 'confirmed-edit-1' });
      await waitFor(() => expect(within(dialog).getByText('保存修改').closest('button')!).not.toHaveClass('ant-btn-loading'));
      await user.click(within(dialog).getByText('保存修改').closest('button')!);
      await waitFor(() => expect(update).toHaveBeenCalledTimes(2));
      expect(update.mock.calls[1]).toEqual([101, first]);
      expect(crypto.randomUUID).toHaveBeenCalledTimes(1);
      await waitFor(() => expect(mockGetTicket).toHaveBeenCalledTimes(3));
    });

    it('requires fresh confirmation after an explicit conflict and uses a new operation', async () => {
      update.mockRejectedValueOnce(Object.assign(new Error('version conflict'), { status: 409, code: 4090 })).mockResolvedValueOnce({ workItemId: 101, version: 10, status: 'open', replayed: false });
      const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
      render(<TicketDetail />);
      await user.click((await screen.findByText('编辑', { selector: 'span' })).closest('button')!);
      let dialog = (await screen.findByText('编辑工单')).closest('[role="dialog"]')! as HTMLElement;
      await user.clear(within(dialog).getByLabelText('工单标题'));
      await user.type(within(dialog).getByLabelText('工单标题'), 'Confirmed title');
      mockGetTicket.mockResolvedValue({ ...baseTicket, recordClass: 'generic', status: 'open', version: 9 });
      await user.click(within(dialog).getByText('保存修改').closest('button')!);
      await waitFor(() => expect(mockGetTicket).toHaveBeenCalledTimes(2));
      expect(update).toHaveBeenCalledTimes(1);
      await user.click((await screen.findByText('编辑', { selector: 'span' })).closest('button')!);
      dialog = (await screen.findByText('编辑工单')).closest('[role="dialog"]')! as HTMLElement;
      await user.clear(within(dialog).getByLabelText('工单标题'));
      await user.type(within(dialog).getByLabelText('工单标题'), 'Confirmed title');
      await user.click(within(dialog).getByText('保存修改').closest('button')!);
      await waitFor(() => expect(update).toHaveBeenCalledTimes(2));
      expect(update.mock.calls[0][1]).toMatchObject({ title: 'Confirmed title', version: 1, operationId: 'confirmed-edit-1' });
      expect(update.mock.calls[1][1]).toMatchObject({ title: 'Confirmed title', version: 9, operationId: 'confirmed-edit-2' });
    });
  });

  it.each(['ENGINEER.WANG', '王工'])('searches assignees by %s and submits the selected identity', async search => {
    mockHasPermission.mockImplementation(permission => permission === 'user:read');
    mockGetTicket.mockResolvedValue({ ...baseTicket, status: 'open' });
    mockGetUsers.mockImplementation(({ search }) => Promise.resolve({ users: search ? [{ id: 7879, name: '王工', username: 'engineer.wang' }] : [{ id: 13, name: '李工', username: 'engineer.li' }] }));
    (TicketApi.assignTicket as jest.Mock).mockResolvedValue({});
    const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
    render(<TicketDetail />);
    await user.click(await screen.findByText('转派分配'));
    const select = screen.getByLabelText('分配给');
    await user.type(select, search);
    await waitFor(() => expect(mockGetUsers).toHaveBeenCalledWith({ pageSize: 100, search }));
    await waitFor(() => expect(screen.queryByText('李工')).not.toBeInTheDocument());
    await user.click(await screen.findByText('王工'));
    await user.click(screen.getByText('确认分配'));
    await waitFor(() => expect(TicketApi.assignTicket).toHaveBeenCalledWith(101, expect.objectContaining({ assigneeId: 7879 })));
  });

  it('ignores late lookup results and retains selected CC identities across searches', async () => {
    mockHasPermission.mockImplementation(permission => permission === 'user:read');
    mockGetTicket.mockResolvedValue({ ...baseTicket, status: 'open' });
    let finishOld!: (value: unknown) => void;
    mockGetUsers.mockImplementation(({ search }) => {
      if (search === 'old') return new Promise(resolve => { finishOld = resolve; });
      return Promise.resolve({ users: search === 'admin'
        ? [{ id: 7879, name: '管理员', username: 'admin' }]
        : search === 'second' ? [{ id: 20, name: '第二人', username: 'second' }] : [] });
    });
    const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
    render(<TicketDetail />);
    await user.click(await screen.findByText('抄送'));
    const select = screen.getByLabelText('抄送给');
    fireEvent.change(select, { target: { value: 'o' } });
    fireEvent.change(select, { target: { value: 'ol' } });
    fireEvent.change(select, { target: { value: 'old' } });
    await waitFor(() => expect(finishOld).toBeDefined());
    expect(mockGetUsers.mock.calls.map(([params]) => params.search)).toEqual(['', 'old']);
    fireEvent.change(select, { target: { value: 'admin' } });
    await user.click(await screen.findByText('管理员', { selector: '.ant-select-item-option-content' }));
    fireEvent.change(select, { target: { value: 'second' } });
    expect(await screen.findByText('第二人', { selector: '.ant-select-item-option-content' })).toBeInTheDocument();
    await act(async () => finishOld({ users: [{ id: 21, name: '过期结果', username: 'old' }] }));
    expect(screen.queryByText('过期结果')).not.toBeInTheDocument();
    expect(screen.getByText('管理员', { selector: '.ant-select-selection-item-content' })).toBeInTheDocument();
    await user.click(screen.getByText('第二人', { selector: '.ant-select-item-option-content' }));
    await user.click(screen.getByText('确认抄送'));
    await waitFor(() => expect(TicketApi.ccTicket).toHaveBeenCalledWith(101, [7879, 20], undefined, ['in_app']));
  });

  it('preserves the current assignee outside the first lookup page', async () => {
    mockHasPermission.mockImplementation(permission => permission === 'user:read');
    mockGetTicket.mockResolvedValue({ ...baseTicket, status: 'open', assigneeId: 7879,
      assignee: { id: 7879, name: '当前处理人', username: 'current' } });
    const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
    render(<TicketDetail />);
    await user.click(await screen.findByText('转派分配'));
    expect((await screen.findByRole('dialog')).querySelector('.ant-select')).toHaveTextContent('当前处理人');
  });

  it('discards a lookup response when user read permission is revoked', async () => {
    mockHasPermission.mockImplementation(permission => permission === 'user:read');
    mockGetTicket.mockResolvedValue({ ...baseTicket, status: 'open' });
    let finish!: (value: unknown) => void;
    mockGetUsers.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
    render(<TicketDetail />);
    await user.click(await screen.findByText('转派分配'));
    mockHasPermission.mockImplementation(() => false);
    await act(async () => finish({ users: [{ id: 7879, name: '受限人员', username: 'restricted' }] }));
    expect(screen.queryByText('受限人员')).not.toBeInTheDocument();
    expect(screen.getByLabelText('分配给')).toBeDisabled();
    expect(await within(await screen.findByRole('dialog')).findByRole('alert')).toHaveTextContent('无权读取人员列表');
  });

  it('shows a retryable user lookup failure in the assignment form', async () => {
    mockHasPermission.mockImplementation(permission => permission === 'user:read');
    mockGetTicket.mockResolvedValue({ ...baseTicket, status: 'open' });
    mockGetUsers.mockRejectedValueOnce(new Error('人员服务不可用')).mockResolvedValueOnce({ users: [{ id: 12, name: '王工', username: 'engineer.wang' }] });
    const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
    render(<TicketDetail />);
    await user.click(await screen.findByText('转派分配'));
    const dialog = await screen.findByRole('dialog');
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('人员服务不可用');
    await user.click(within(dialog).getByRole('button', { name: '重试人员列表' }));
    await waitFor(() => expect(mockGetUsers).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(within(dialog).queryByRole('alert')).not.toBeInTheDocument());
  });  it('shows the shared user error in the CC form without bypassing read permission', async () => {
    mockGetTicket.mockResolvedValue({ ...baseTicket, status: 'open' });
    const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
    render(<TicketDetail />);
    await user.click(await screen.findByText('抄送'));
    const dialog = await screen.findByRole('dialog');
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('无权读取人员列表');
    expect(mockGetUsers).not.toHaveBeenCalled();
    expect(within(dialog).queryByRole('button', { name: '重试人员列表' })).not.toBeInTheDocument();
  });});

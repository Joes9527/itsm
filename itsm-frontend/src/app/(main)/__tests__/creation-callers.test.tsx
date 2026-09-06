// These integration cases render complete Ant Design forms under coverage instrumentation.
jest.setTimeout(30000);
import React from 'react';
import { render, screen, fireEvent, waitFor, within } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import IncidentPage from '../incidents/create/page';
import ProblemPage from '../problems/new/page';
import ChangePage from '../changes/new/page';
import ImprovementPage from '../improvements/new/page';
import StandardChangesPage from '../standard-changes/page';
import { IncidentManagement } from '@/components/business/IncidentManagement';
import { IncidentAPI } from '@/lib/api/incident-api';
import { ProblemApi } from '@/lib/api/problem-api';
import { ChangeApi } from '@/lib/api/change-api';
import { TicketApi } from '@/lib/api/ticket-api';
import { StandardChangeApi } from '@/lib/api/standard-change-api';
import { useAuthStore } from '@/lib/store/auth-store';
import { creationReceipt } from '@/lib/api/creation.test-utils';
jest.unmock('dayjs');
const push = jest.fn();
jest.mock('next/navigation', () => ({
  useRouter: () => ({ push, back: jest.fn() }),
  useSearchParams: () => new URLSearchParams(),
  useParams: () => ({}),
}));
jest.mock('@/lib/i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }));
jest.mock('@/lib/api/incident-api', () => ({
  IncidentAPI: {
    createIncident: jest.fn(),
    listIncidents: jest.fn().mockResolvedValue({ incidents: [], total: 0 }),
  },
}));
jest.mock('@/lib/api/problem-api', () => ({ ProblemApi: { createProblem: jest.fn() } }));
jest.mock('@/lib/api/change-api', () => ({ ChangeApi: { createChange: jest.fn() } }));
jest.mock('@/lib/api/ticket-api', () => ({ TicketApi: { createTicket: jest.fn() } }));
jest.mock('@/lib/api/user-api', () => ({
  UserApi: { getUsers: jest.fn().mockResolvedValue({ users: [] }) },
}));
jest.mock('@/lib/api/standard-change-api', () => ({
  StandardChangeApi: {
    instantiate: jest.fn(),
    getTemplates: jest.fn(),
    getCategories: jest.fn().mockResolvedValue({ categories: [] }),
  },
}));
let key = 0;
Object.defineProperty(crypto, 'randomUUID', { configurable: true, value: () => `caller-${++key}` });
beforeEach(() => {
  jest.clearAllMocks();
  useAuthStore.setState({
    isAuthenticated: true,
    user: { id: 1, actorTenantId: 2, tenantId: 2 } as never,
    currentTenant: { id: 2 } as never,
  });
});
function fill(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}
it('Incident page submits supported fields with manual source and presents pending receipt', async () => {
  jest.mocked(IncidentAPI.createIncident).mockResolvedValue(creationReceipt);
  render(<IncidentPage />);
  fill('事件标题', '数据库连接失败');
  fill('详细描述', '生产服务连接数据库出现持续错误');
  fireEvent.click(screen.getByTestId('incident-submit-button'));
  await waitFor(() => expect(push).toHaveBeenCalledWith('/incidents'));
  expect(IncidentAPI.createIncident).toHaveBeenCalledWith(
    expect.objectContaining({
      title: '数据库连接失败',
      source: 'manual',
      configurationItemIds: [],
    }),
    expect.objectContaining({ idempotencyKey: expect.any(String) })
  );
  expect(screen.getAllByText(/WI-41.*流程启动排队中/).length).toBeGreaterThan(0);
});
it('Problem page preserves root cause/impact and does not imply unsupported assignment', async () => {
  jest.mocked(ProblemApi.createProblem).mockResolvedValue(creationReceipt);
  render(<ProblemPage />);
  fill('问题标题', '持续连接失败');
  fill('详细描述', '生产服务连接数据库出现持续错误');
  fill('根本原因分析 (RCA)', '数据库连接池资源长期未能释放');
  fill('影响范围', '影响当前全部生产系统的业务请求');
  fireEvent.click(screen.getByRole('button', { name: '创建问题' }));
  await waitFor(() => expect(push).toHaveBeenCalledWith('/problems'));
  expect(ProblemApi.createProblem).toHaveBeenCalledWith(
    expect.objectContaining({
      rootCause: '数据库连接池资源长期未能释放',
      impact: '影响当前全部生产系统的业务请求',
    }),
    expect.anything()
  );
  expect(jest.mocked(ProblemApi.createProblem).mock.calls[0][0]).not.toHaveProperty('assigneeId');
});
it('Change page preserves the complete professional input and reports manual intervention as committed', async () => {
  jest
    .mocked(ChangeApi.createChange)
    .mockResolvedValue({ ...creationReceipt, workflowStartStatus: 'manual_intervention_required' });
  render(<ChangePage />);
  fill('变更标题', '数据库升级');
  fill('详细描述', '升级数据库以解决连接池故障');
  fill('变更理由', '恢复数据库服务可靠性');
  fill('实施计划', '备份数据库并升级验证');
  fill('回滚计划', '回退数据库并恢复备份');
  fireEvent.click(screen.getByTestId('change-submit-button'));
  await waitFor(() => expect(push).toHaveBeenCalledWith('/changes'));
  expect(ChangeApi.createChange).toHaveBeenCalledWith(
    expect.objectContaining({
      justification: '恢复数据库服务可靠性',
      implementationPlan: '备份数据库并升级验证',
      rollbackPlan: '回退数据库并恢复备份',
      type: 'normal',
      affectedCis: [],
      relatedTickets: [],
    }),
    expect.anything()
  );
  expect(screen.getAllByText(/WI-41.*已创建.*需要人工处理/).length).toBeGreaterThan(0);
});
it('Improvement remains explicit generic and consumes receipt', async () => {
  jest.mocked(TicketApi.createTicket).mockResolvedValue(creationReceipt);
  render(<ImprovementPage />);
  fill('标题', '改进服务指标');
  fill('目标描述', '为服务增加可追踪的度量指标');
  fireEvent.click(screen.getByRole('button', { name: '保存' }));
  await waitFor(() => expect(push).toHaveBeenCalledWith('/improvements'));
  expect(TicketApi.createTicket).toHaveBeenCalledWith(
    expect.objectContaining({ type: 'improvement', title: '改进服务指标' }),
    expect.anything()
  );
});
it('Standard Change uses professional reference, never shared workItemId, for navigation', async () => {
  jest
    .mocked(StandardChangeApi.getTemplates)
    .mockResolvedValue({
      templates: [
        {
          id: 7,
          title: '标准升级',
          category: 'database',
          riskLevel: 'low',
          affectedCIs: [],
          isActive: true,
        },
      ],
      total: 1,
    } as never);
  jest
    .mocked(StandardChangeApi.instantiate)
    .mockResolvedValue({
      ...creationReceipt,
      recordClass: 'change_request',
      professionalReference: { type: 'change', id: 88 },
    });
  render(<StandardChangesPage />);
  await screen.findByText('标准升级');
  const button = screen.getByRole('button', { name: '从模板创建变更' });
  await userEvent.click(button);
  const dialog = await screen.findByRole('dialog');
  fireEvent.click(within(dialog).getByRole('button', { name: /创|确|OK/ }));
  await waitFor(() => expect(push).toHaveBeenCalledWith('/changes/88'));
  expect(StandardChangeApi.instantiate).toHaveBeenCalledWith(
    7,
    expect.objectContaining({ title: '标准升级' }),
    expect.anything()
  );
});
it('IncidentManagement modal consumes the shared receipt for its active create owner', async () => {
  jest.mocked(IncidentAPI.createIncident).mockResolvedValue(creationReceipt);
  render(<IncidentManagement />);
  await userEvent.click(screen.getByRole('button', { name: '创建事件' }));
  const dialog = await screen.findByRole('dialog');
  fireEvent.change(within(dialog).getByLabelText('事件标题'), {
    target: { value: '监控服务中断' },
  });
  // This compact form requires priority and severity explicitly.
  for (const label of ['优先级', '严重程度']) {
    const input = within(dialog).getByLabelText(label);
    await userEvent.click(input);
    const dropdown = document.getElementById(input.getAttribute('aria-controls')!)!.closest('.ant-select-dropdown') as HTMLElement;
    await waitFor(() => expect(dropdown).toBeVisible());
    await userEvent.click(within(dropdown).getByText('高', { selector: '.ant-select-item-option-content' }));
  }
  fireEvent.click(within(dialog).getByRole('button', { name: /确|OK/ }));
  await waitFor(() => expect(IncidentAPI.createIncident).toHaveBeenCalledTimes(1));
  expect(IncidentAPI.createIncident).toHaveBeenCalledWith(
    expect.objectContaining({ title: '监控服务中断', severity: 'high', source: 'manual' }),
    expect.anything()
  );
  expect(screen.getAllByText(/WI-41.*流程启动排队中/).length).toBeGreaterThan(0);
});
it('Incident entry only collects accepted inputs and directs attachments to the shared WorkItem detail', async () => {
  render(<IncidentPage />);
  await userEvent.click(screen.getByRole('tab', { name: '影响分析' }));
  expect(screen.queryByLabelText('受影响系统')).not.toBeInTheDocument();
  expect(screen.queryByLabelText('初步原因分析')).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole('tab', { name: '附件' }));
  expect(screen.getByText('创建后，可在关联工单详情的附件区上传文件。')).toBeInTheDocument();
  expect(screen.queryByLabelText('上传附件')).not.toBeInTheDocument();
});

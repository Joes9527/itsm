import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import IncidentDetail from '../IncidentDetail';
import { WorkItemProvider } from '@/components/work-item/WorkItemContext';
import type { WorkItemActionState, WorkItemCommon } from '@/components/work-item/WorkItemTypes';

const mockGetIncident = jest.fn();
const mockGetRootCauseAnalysis = jest.fn();
const mockGetImpactAssessment = jest.fn();
const mockGetIncidentClassification = jest.fn();
const mockResolveIncident = jest.fn();
const mockCloseIncident = jest.fn();
const hasPermission = () => false;

jest.mock('next/navigation', () => ({
  useParams: () => ({ id: '301' }),
  useRouter: () => ({ push: jest.fn(), back: jest.fn() }),
}));

jest.mock('@/lib/api/', () => ({
  IncidentAPI: {
    getIncident: (...args: unknown[]) => mockGetIncident(...args),
    getRootCauseAnalysis: (...args: unknown[]) => mockGetRootCauseAnalysis(...args),
    getImpactAssessment: (...args: unknown[]) => mockGetImpactAssessment(...args),
    getIncidentClassification: (...args: unknown[]) => mockGetIncidentClassification(...args),
    resolveIncident: (...args: unknown[]) => mockResolveIncident(...args),
    closeIncident: (...args: unknown[]) => mockCloseIncident(...args),
  },
}));

jest.mock('@/lib/api/user-api', () => ({
  UserApi: {
    getUsers: jest.fn(),
  },
}));

jest.mock('@/lib/store/auth-store', () => ({
  useAuthStore: (selector: (state: { hasPermission: typeof hasPermission }) => unknown) =>
    selector({ hasPermission }),
}));

const workItem: WorkItemCommon = {
  id: 301,
  number: 'INC-202608-000301',
  recordClass: 'incident',
  title: '数据库连接失败',
  status: 'closed',
  priority: 'high',
  requesterId: 9,
  createdAt: '2026-08-28T00:00:00Z',
  updatedAt: '2026-08-28T00:00:00Z',
};

const incident = {
  id: 301,
  incidentNumber: 'INC-202608-000301',
  title: '数据库连接失败',
  description: '生产数据库连接失败',
  status: 'closed',
  priority: 'high',
  severity: 'high',
  category: 'database',
  subcategory: 'connection',
  source: 'monitoring',
  type: 'service',
};

const deniedActions = {
  edit: { allowed: false, reason: '无权编辑该事件' },
  resolve: { allowed: false, reason: '只有处理中的事件可以解决' },
  close: { allowed: false, reason: '只有已解决的事件可以关闭' },
  reopen: { allowed: false, reason: '当前事件不能重新打开' },
  escalate: { allowed: false, reason: '当前事件不能升级' },
  assign: { allowed: false, reason: '无权指派该事件' },
  markMajorIncident: { allowed: false, reason: '当前事件不能标记为重大事件' },
  convertToProblem: { allowed: false, reason: '当前事件不能转为问题' },
} satisfies Record<string, WorkItemActionState>;

function renderWithProvider(
  actions: Record<string, WorkItemActionState>,
  fallbackActions?: Record<string, WorkItemActionState>
) {
  return render(
    <WorkItemProvider value={{ workItem, actions, onActionDispatch: jest.fn() }}>
      <IncidentDetail id='301' fallbackActions={fallbackActions} />
    </WorkItemProvider>
  );
}

function renderWithoutProvider(fallbackActions?: Record<string, WorkItemActionState>) {
  return render(<IncidentDetail id='301' fallbackActions={fallbackActions} />);
}

function IncidentDetailProviderHarness() {
  const [currentWorkItem, setCurrentWorkItem] = React.useState<WorkItemCommon>({
    ...workItem,
    status: 'in_progress',
  });
  const [currentActions, setCurrentActions] = React.useState<Record<string, WorkItemActionState>>({
    resolve: { allowed: true },
  });
  const handleIncidentLoaded = React.useCallback((loaded: unknown) => {
    const loadedIncident = loaded as typeof incident & {
      actions?: Record<string, WorkItemActionState>;
    };
    setCurrentWorkItem(prev => ({
      ...prev,
      status: loadedIncident.status,
      title: loadedIncident.title,
    }));
    setCurrentActions(loadedIncident.actions ?? {});
  }, []);

  return (
    <WorkItemProvider
      value={{ workItem: currentWorkItem, actions: currentActions, onActionDispatch: jest.fn() }}
    >
      <IncidentDetail id='301' onIncidentLoaded={handleIncidentLoaded} />
    </WorkItemProvider>
  );
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

describe('IncidentDetail action eligibility', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetIncident.mockResolvedValue(incident);
    mockGetRootCauseAnalysis.mockResolvedValue(null);
    mockGetImpactAssessment.mockResolvedValue(null);
    mockGetIncidentClassification.mockResolvedValue(null);
    mockResolveIncident.mockResolvedValue(incident);
    mockCloseIncident.mockResolvedValue(incident);
  });

  it('disables every denied incident action and shows the backend reason', async () => {
    renderWithProvider(deniedActions);

    await expectDisabledAction('编辑', deniedActions.edit.reason);
    await expectDisabledAction('解决', deniedActions.resolve.reason);
    await expectDisabledAction('关闭', deniedActions.close.reason);
    await expectDisabledAction('重新打开', deniedActions.reopen.reason);
    await expectDisabledAction('升级', deniedActions.escalate.reason);
    await expectDisabledAction('指派', deniedActions.assign.reason);
    await expectDisabledAction('升级为重大事件', deniedActions.markMajorIncident.reason);
    await expectDisabledAction('转为问题', deniedActions.convertToProblem.reason);
  });

  it('prefers work item context actions over fallback actions when a provider is present', async () => {
    renderWithProvider(
      { resolve: { allowed: false, reason: '上下文动作优先' } },
      { resolve: { allowed: true } }
    );

    await expectDisabledAction('解决', '上下文动作优先');
  });

  it('calls the existing resolve endpoint when the backend allows resolve', async () => {
    renderWithProvider({ resolve: { allowed: true } });

    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '解决' }));

    const dialog = await screen.findByRole('dialog', { name: '解决事件' });
    await user.type(within(dialog).getByLabelText('解决方案'), '已恢复数据库连接并验证服务正常');
    await user.click(within(dialog).getByRole('button', { name: '确认解决' }));

    await waitFor(() =>
      expect(mockResolveIncident).toHaveBeenCalledWith(301, {
        resolution: '已恢复数据库连接并验证服务正常',
        resolutionCode: undefined,
      })
    );
  });

  it('uses fallback actions without throwing when no WorkItemProvider exists', async () => {
    renderWithoutProvider({ reopen: { allowed: false, reason: '历史事件未回填 WorkItem' } });

    await expectDisabledAction('重新打开', '历史事件未回填 WorkItem');
    expect(mockGetIncident).toHaveBeenCalledWith(301);
  });

  it('refreshes provider actions from the backend detail response after resolve', async () => {
    mockGetIncident
      .mockResolvedValueOnce({
        ...incident,
        status: 'in_progress',
        actions: { resolve: { allowed: true } },
      })
      .mockResolvedValueOnce({
        ...incident,
        status: 'resolved',
        actions: {
          resolve: { allowed: false, reason: '只有处理中的事件可以解决' },
          close: { allowed: true },
        },
      });

    const user = userEvent.setup();
    render(<IncidentDetailProviderHarness />);

    await user.click(await screen.findByRole('button', { name: '解决' }));

    const dialog = await screen.findByRole('dialog', { name: '解决事件' });
    fireEvent.change(within(dialog).getByLabelText('解决方案'), {
      target: { value: '已恢复数据库连接并验证服务正常' },
    });
    await user.click(within(dialog).getByRole('button', { name: '确认解决' }));

    await waitFor(() => expect(mockGetIncident).toHaveBeenCalledTimes(2));
    expect(await screen.findByRole('button', { name: /关\s*闭/ })).toBeEnabled();
    await expectDisabledAction('解决', '只有处理中的事件可以解决');
  });

  it('disables sibling actions while an incident mutation is in flight', async () => {
    mockCloseIncident.mockImplementation(() => new Promise(() => {}));
    renderWithProvider({
      close: { allowed: true },
      reopen: { allowed: true },
    });

    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /关\s*闭/ }));
    await waitFor(() => expect(mockCloseIncident).toHaveBeenCalledWith(301));

    expect(screen.getByRole('button', { name: '重新打开' })).toBeDisabled();
  });
});

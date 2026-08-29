import React from 'react';
import { render, screen, waitFor, within } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import ProblemDetail from '../ProblemDetail';
import { WorkItemProvider } from '@/components/work-item/WorkItemContext';
import type { WorkItemActionState, WorkItemCommon } from '@/components/work-item/WorkItemTypes';
import { ProblemStatus } from '@/constants/problem';

const mockGetProblem = jest.fn();
const mockUpdateProblem = jest.fn();
const mockPush = jest.fn();

jest.mock('next/navigation', () => ({
  useParams: () => ({ id: '401' }),
  useRouter: () => ({ push: mockPush, back: jest.fn() }),
}));

jest.mock('@/lib/api/', () => ({
  ProblemApi: {
    getProblem: (...args: unknown[]) => mockGetProblem(...args),
    updateProblem: (...args: unknown[]) => mockUpdateProblem(...args),
  },
}));

jest.mock('../ProblemInvestigationTab', () => ({
  __esModule: true,
  default: () => <div>ProblemInvestigationTab</div>,
}));

jest.mock('../BasicInfoCard', () => ({
  __esModule: true,
  default: () => <div>BasicInfoCard</div>,
}));

const workItem: WorkItemCommon = {
  id: 401,
  number: 'PRB-202608-000401',
  recordClass: 'problem',
  title: '重复性网络抖动',
  status: 'open',
  priority: 'high',
  requesterId: 11,
  createdAt: '2026-08-28T00:00:00Z',
  updatedAt: '2026-08-28T00:00:00Z',
};

const problem = {
  id: 401,
  title: '重复性网络抖动',
  description: '多个站点出现重复性网络抖动',
  status: 'open',
  priority: 'high',
  severity: 'high',
  category: 'network',
  createdBy: 11,
  createdAt: '2026-08-28T00:00:00Z',
  updatedAt: '2026-08-28T00:00:00Z',
};

function renderWithWorkItemContext(
  actions: Record<string, WorkItemActionState>,
  fallbackActions?: Record<string, WorkItemActionState>
) {
  return render(
    <WorkItemProvider value={{ workItem, actions, onActionDispatch: jest.fn() }}>
      <ProblemDetail id="401" fallbackActions={fallbackActions} />
    </WorkItemProvider>
  );
}

function renderWithoutProvider(fallbackActions?: Record<string, WorkItemActionState>) {
  return render(<ProblemDetail id="401" fallbackActions={fallbackActions} />);
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

describe('ProblemDetail action eligibility', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetProblem.mockResolvedValue(problem);
    mockUpdateProblem.mockResolvedValue({ ...problem, status: ProblemStatus.INVESTIGATING });
  });

  it('starts investigation with the canonical investigating status', async () => {
    renderWithWorkItemContext({
      edit: { allowed: true },
      start_investigation: { allowed: true },
      resolve: { allowed: false, reason: '只有调查中的问题可以标记解决' },
      close: { allowed: false, reason: '只有已解决的问题可以关闭' },
    });

    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: '开始调查' }));

    await waitFor(() =>
      expect(mockUpdateProblem).toHaveBeenCalledWith(401, expect.objectContaining({ status: ProblemStatus.INVESTIGATING }))
    );
  });

  it('prefers provider actions over fallback actions and exposes denied reasons', async () => {
    renderWithWorkItemContext(
      { start_investigation: { allowed: false, reason: '上下文动作优先' } },
      { start_investigation: { allowed: true } }
    );

    await expectDisabledAction('开始调查', '上下文动作优先');
  });

  it('uses fallback actions without a WorkItemProvider', async () => {
    renderWithoutProvider({ resolve: { allowed: false, reason: '历史问题未回填 WorkItem 动作' } });

    await expectDisabledAction('标记解决', '历史问题未回填 WorkItem 动作');
    expect(mockGetProblem).toHaveBeenCalledWith(401);
  });

  it('fails closed when the backend omits problem actions', async () => {
    renderWithoutProvider();

    await screen.findByText('重复性网络抖动');
    expect(screen.queryByRole('button', { name: '开始调查' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '标记解决' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '关闭问题' })).not.toBeInTheDocument();
  });
});

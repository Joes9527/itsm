import React from 'react';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import IncidentPage from '../incidents/[id]/page';
import ProblemPage from '../problems/[id]/page';
import ChangePage from '../changes/[id]/page';
import { IncidentAPI } from '@/lib/api/incident-api';
import { ProblemApi } from '@/lib/api/problem-api';
import { ChangeApi } from '@/lib/api/change-api';
import { TicketApi } from '@/lib/api/ticket-api';

jest.mock('next/navigation', () => ({
  useParams: () => ({ id: '4' }),
  useRouter: () => ({ back: jest.fn() }),
}));
jest.mock('@/components/incident/IncidentDetail', () => ({
  __esModule: true,
  default: ({ id, onIncidentLoaded }: any) => (
    <div data-testid='professional'>
      incident:{id}
      <button
        onClick={() =>
          onIncidentLoaded({
            id: 4,
            workItemId: 91,
            number: 'TKT-0091',
            version: 8,
            title: 'record',
            status: 'open',
            priority: 'high',
            createdBy: 1,
            actions: { assign: { allowed: true } },
          })
        }
      >
        reload professional incident
      </button>
    </div>
  ),
}));
jest.mock('@/components/problem/ProblemDetail', () => ({
  __esModule: true,
  default: ({ id, onProblemLoaded }: any) => (
    <div data-testid='professional'>
      problem:{id}
      <button
        onClick={() =>
          onProblemLoaded({
            id: 4,
            workItemId: 91,
            number: 'TKT-0091',
            version: 8,
            title: 'record',
            status: 'open',
            priority: 'high',
            createdBy: 1,
            actions: { assign: { allowed: true } },
          })
        }
      >
        reload professional problem
      </button>
    </div>
  ),
}));
jest.mock('@/components/change/ChangeDetail', () => ({
  __esModule: true,
  default: ({ id }: { id: string }) => <div data-testid='professional'>change:{id}</div>,
}));
jest.mock('@/components/business/detail-tabs', () => ({ ApprovalTimeline: () => null }));
jest.mock('@/components/work-item/WorkItemShell', () => ({
  WorkItemShell: ({ workItem, professionalPanelSlot, assignment }: any) => (
    <div>
      {assignment && (
        <button
          onClick={() =>
            assignment.submit({
              assigneeId: 8,
              reason: 'team handover',
              version: 7,
              operationId: 'intent-1',
            })
          }
        >
          submit handover
        </button>
      )}
      <output data-testid='identity'>{JSON.stringify(workItem)}</output>
      {professionalPanelSlot}
    </div>
  ),
}));
jest.mock('@/lib/api/incident-api', () => ({
  IncidentAPI: { getIncident: jest.fn(), assignIncident: jest.fn().mockResolvedValue({}) },
}));
jest.mock('@/lib/api/problem-api', () => ({
  ProblemApi: { getProblem: jest.fn(), updateProblem: jest.fn().mockResolvedValue({}) },
}));
jest.mock('@/lib/api/change-api', () => ({
  ChangeApi: {
    getChange: jest.fn(),
    assignChange: jest.fn().mockResolvedValue({}),
    getChangeApprovals: jest.fn().mockResolvedValue([]),
  },
}));
jest.mock('@/lib/api/user-api', () => ({
  UserApi: {
    getUsers: jest
      .fn()
      .mockResolvedValue({
        users: [{ id: 8, name: 'Next', active: true }],
        pagination: { page: 1, pageSize: 100, total: 1, totalPages: 1 },
      }),
  },
}));
jest.mock('@/lib/api/ticket-api', () => ({
  TicketApi: { getTicketSLA: jest.fn().mockResolvedValue({}) },
}));
const valid = {
  id: 4,
  workItemId: 91,
  number: 'TKT-0091',
  incidentNumber: 'TKT-0091',
  version: 7,
  title: 'record',
  status: 'in_progress',
  priority: 'high',
  createdBy: 1,
  actions: { assign: { allowed: true } },
};
beforeEach(() => jest.clearAllMocks());
for (const [name, Page, get] of [
  ['incident', IncidentPage, IncidentAPI.getIncident],
  ['problem', ProblemPage, ProblemApi.getProblem],
  ['change', ChangePage, ChangeApi.getChange],
] as const) {
  it(`${name}: carries WorkItem identity and observed version while panel retains professional ID`, async () => {
    jest.mocked(get).mockResolvedValue(valid as never);
    render(<Page />);
    const identity = JSON.parse((await screen.findByTestId('identity')).textContent!);
    expect(identity).toMatchObject({ id: 91, number: 'TKT-0091', version: 7 });
    expect(screen.getByTestId('professional')).toHaveTextContent(`${name}:4`);
    await waitFor(() => expect(TicketApi.getTicketSLA).toHaveBeenCalledWith(91));
    expect(get).toHaveBeenCalledWith(4);
  });
  it(`${name}: invalid identity blocks professional actions instead of guessing`, async () => {
    jest.mocked(get).mockResolvedValue({ ...valid, version: 0 } as never);
    render(<Page />);
    await screen.findByText(/身份|版本/);
    expect(screen.queryByTestId('identity')).not.toBeInTheDocument();
    expect(screen.queryByTestId('professional')).not.toBeInTheDocument();
    expect(TicketApi.getTicketSLA).not.toHaveBeenCalled();
  });
}

for (const [name, Page, get, assign, payload] of [
  [
    'incident',
    IncidentPage,
    IncidentAPI.getIncident,
    IncidentAPI.assignIncident,
    { assigneeId: 8, reason: 'team handover', version: 7, operationId: 'intent-1' },
  ],
  [
    'problem',
    ProblemPage,
    ProblemApi.getProblem,
    ProblemApi.updateProblem,
    { assigneeId: 8, assignmentReason: 'team handover', version: 7, operationId: 'intent-1' },
  ],
  [
    'change',
    ChangePage,
    ChangeApi.getChange,
    ChangeApi.assignChange,
    {
      assigneeId: 8,
      assignmentReason: 'team handover',
      expectedVersion: 7,
      operationId: 'intent-1',
    },
  ],
] as const) {
  it(`${name}: real page submits professional ID 4 with observed version and domain reason`, async () => {
    jest.mocked(get).mockResolvedValue(valid as never);
    render(<Page />);
    fireEvent.click(await screen.findByRole('button', { name: 'submit handover' }));
    await waitFor(() => expect(assign).toHaveBeenCalledWith(4, payload));
    expect(assign).not.toHaveBeenCalledWith(91, expect.anything());
  });
}

for (const [name, Page, get] of [
  ['incident', IncidentPage, IncidentAPI.getIncident],
  ['problem', ProblemPage, ProblemApi.getProblem],
] as const) {
  it(`${name}: reloads SLA when a professional command changes version on the same WorkItem`, async () => {
    jest.mocked(get).mockResolvedValue(valid as never);
    render(<Page />);
    await waitFor(() => expect(TicketApi.getTicketSLA).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: `reload professional ${name}` }));
    await waitFor(() => expect(TicketApi.getTicketSLA).toHaveBeenCalledTimes(2));
    expect(TicketApi.getTicketSLA).toHaveBeenLastCalledWith(91);
    expect(JSON.parse(screen.getByTestId('identity').textContent!).version).toBe(8);
  });
}

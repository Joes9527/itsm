import React from 'react';
import { render, screen } from '@testing-library/react';
import { WorkItemSLA } from '../WorkItemSLA';
import { mapWorkItemSLA } from '../mapWorkItemSLA';
import type { TicketSLAInfo } from '@/lib/api/ticket-api';
import type { WorkItemSLAState } from '../WorkItemTypes';

describe('WorkItemSLA', () => {
  it('renders nothing when sla is undefined', () => {
    const { container } = render(<WorkItemSLA />);
    expect(container).toBeEmptyDOMElement();
  });

  it('shows explicit no-SLA state', () => {
    const sla: WorkItemSLAState = {
      slaName: '',
      slaStatus: 'not_required',
      responseTime: 0,
      resolutionTime: 0,
      responseDeadline: null,
      resolutionDeadline: null,
      responseTimeRemaining: null,
      resolutionTimeRemaining: null,
      isBreached: false,
    };
    render(<WorkItemSLA sla={sla} />);
    expect(screen.getByText('无需 SLA')).toBeInTheDocument();
  });

  it('renders the SLA name and deadlines when sla is present', () => {
    const sla: WorkItemSLAState = {
      slaName: '标准 SLA',
      responseTime: 60,
      resolutionTime: 480,
      responseDeadline: '2026-08-28T10:00:00Z',
      resolutionDeadline: '2026-08-28T18:00:00Z',
      responseTimeRemaining: 30,
      resolutionTimeRemaining: 200,
      isBreached: false,
    };
    render(<WorkItemSLA sla={sla} />);
    expect(screen.getByText('标准 SLA')).toBeInTheDocument();
    expect(screen.getByText('响应截止:')).toBeInTheDocument();
    expect(screen.getByText('解决截止:')).toBeInTheDocument();
  });

  it('highlights an overdue response and shows the breach tag', () => {
    const sla: WorkItemSLAState = {
      slaName: '标准 SLA',
      responseTime: 60,
      resolutionTime: 480,
      responseDeadline: '2026-08-28T10:00:00Z',
      resolutionDeadline: null,
      responseTimeRemaining: -15,
      resolutionTimeRemaining: null,
      isBreached: true,
    };
    render(<WorkItemSLA sla={sla} />);
    expect(screen.getByText(/已超时/)).toBeInTheDocument();
    expect(screen.getByText('SLA 已违规')).toBeInTheDocument();
  });
});

it('shows configuration failure instead of success', () => {
  render(
    <WorkItemSLA
      sla={{
        slaName: '',
        slaStatus: 'configuration_missing',
        responseTime: 0,
        resolutionTime: 0,
        responseDeadline: null,
        resolutionDeadline: null,
        responseTimeRemaining: null,
        resolutionTimeRemaining: null,
        isBreached: false,
      }}
    />
  );
  expect(screen.getByText('SLA 配置缺失')).toBeInTheDocument();
});
it('shows current and historical facts and marks completed clocks', () => {
  render(
    <WorkItemSLA
      sla={{
        slaName: '标准 SLA',
        slaStatus: 'ok',
        cycleNumber: 2,
        cycleStartedAt: '2026-09-11T08:00:00Z',
        pausedMinutes: 5,
        responseTime: 60,
        resolutionTime: 480,
        responseDeadline: '2026-09-11T09:00:00Z',
        resolutionDeadline: '2026-09-11T16:00:00Z',
        firstResponseAt: '2026-09-11T08:10:00Z',
        resolvedAt: '2026-09-11T09:00:00Z',
        responseTimeRemaining: 50,
        resolutionTimeRemaining: 420,
        isBreached: false,
        history: [
          {
            number: 1,
            startedAt: '2026-09-10T08:00:00Z',
            endedAt: '2026-09-11T08:00:00Z',
            responseAt: null,
            resolvedAt: '2026-09-10T18:00:00Z',
            responseDeadline: null,
            resolutionDeadline: '2026-09-10T16:00:00Z',
            pausedMinutes: 0,
            responseBreached: false,
            resolutionBreached: true,
            policy: null,
            actorId: 7,
            source: 'http',
            correlationId: 'cycle-2',
          },
        ],
      }}
    />
  );
  expect(screen.getByText('当前周期 #2')).toBeInTheDocument();
  expect(screen.getByText('历史周期 #1')).toBeInTheDocument();
  expect(screen.getByText('解决已违规')).toBeInTheDocument();
  expect(screen.getByText(/已响应/)).toBeInTheDocument();
  expect(screen.getByText(/已解决/)).toBeInTheDocument();
  expect(screen.queryByText(/剩余/)).not.toBeInTheDocument();
});

it('preserves the backend SLA cycle projection in the shared mapper', () => {
  const info = {
    slaStatus: 'not_required',
    cycleNumber: 2,
    history: [{ number: 1, actorId: 4, source: 'http', correlationId: 'reopen-2' }],
    closedAt: '2026-09-11T09:00:00Z',
  } as TicketSLAInfo;
  expect(mapWorkItemSLA(info)).toEqual(info);
  expect(mapWorkItemSLA(null)).toBeUndefined();
});

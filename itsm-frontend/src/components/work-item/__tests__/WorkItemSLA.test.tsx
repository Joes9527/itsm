import React from 'react';
import { render, screen } from '@testing-library/react';
import { WorkItemSLA } from '../WorkItemSLA';
import type { WorkItemSLAState } from '../WorkItemTypes';

describe('WorkItemSLA', () => {
  it('renders nothing when sla is undefined', () => {
    const { container } = render(<WorkItemSLA />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders nothing when sla has no deadlines and no positive time targets (backend no-SLA fallback)', () => {
    const sla: WorkItemSLAState = {
      slaName: '默认SLA',
      responseTime: 0,
      resolutionTime: 0,
      responseDeadline: null,
      resolutionDeadline: null,
      responseTimeRemaining: null,
      resolutionTimeRemaining: null,
      isBreached: false,
    };
    const { container } = render(<WorkItemSLA sla={sla} />);
    expect(container).toBeEmptyDOMElement();
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

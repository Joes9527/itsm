import { render, screen, waitFor } from '@/lib/test-utils';
import ServiceRequestList from '../ServiceRequestList';
import { serviceRequestAPI } from '@/lib/api/service-request-api';

// ServiceRequestList used to have an internal "待办审批" tab backed by
// serviceRequestAPI.getPendingApprovals, which hit the SR-approval route Task 1 deleted
// (approval now flows through the linked ticket's own BPMN mechanism instead). That tab, and
// the getPendingApprovals method itself, have since been removed entirely (final review fix
// wave) — this locks in that only "我的请求" remains.
jest.mock('@/lib/api/service-request-api', () => ({
  serviceRequestAPI: {
    getServiceRequests: jest.fn(),
  },
}));

const mockGetServiceRequests = serviceRequestAPI.getServiceRequests as jest.Mock;

describe('ServiceRequestList', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders only "我的请求" and has no per-row 审批 action', async () => {
    mockGetServiceRequests.mockResolvedValue({
      requests: [
        {
          id: 1,
          ticketId: 7,
          ticketTitle: '申请一台云主机',
          ticketStatus: 'open',
          createdAt: '2026-08-01T00:00:00Z',
        },
      ],
      total: 1,
    });

    render(<ServiceRequestList />);

    await waitFor(() => expect(mockGetServiceRequests).toHaveBeenCalled());
    expect(await screen.findByText('申请一台云主机')).toBeInTheDocument();

    expect(screen.getByText('我的请求')).toBeInTheDocument();
    expect(screen.queryByText('待办审批')).not.toBeInTheDocument();
    expect(screen.queryByTitle('审批')).not.toBeInTheDocument();
  });
});

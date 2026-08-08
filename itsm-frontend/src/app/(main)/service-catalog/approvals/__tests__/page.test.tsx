import { render, waitFor } from '@testing-library/react';
import ServiceCatalogApprovalsRedirectPage from '../page';

// This page used to be a standalone "服务请求审批" list+approve/reject page, calling
// ServiceCatalogApi.getServiceRequests({status: pending_approval})/approveServiceRequest/
// rejectServiceRequest — all routes Task 1 deleted (SR approval retired in favor of the
// linked ticket's own BPMN process). It's now a plain redirect to /approvals/pending, the
// single correct "我的待办审批" entry point. This locks in the redirect target.
const mockReplace = jest.fn();

jest.mock('next/navigation', () => ({
  useRouter: () => ({
    push: jest.fn(),
    replace: mockReplace,
  }),
}));

describe('ServiceCatalogApprovalsRedirectPage', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('redirects to /approvals/pending without fetching any data first', async () => {
    render(<ServiceCatalogApprovalsRedirectPage />);

    await waitFor(() => expect(mockReplace).toHaveBeenCalledWith('/approvals/pending'));
  });
});

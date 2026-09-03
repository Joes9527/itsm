import React from 'react';
import '@testing-library/jest-dom';

const mockPush = jest.fn();
const mockBack = jest.fn();
const mockCreateIncident = jest.fn();
const mockGetUsers = jest.fn();
const mockSearchCIs = jest.fn();
const mockHandleError = jest.fn();

jest.mock('next/navigation', () => ({
  useRouter: () => ({ push: mockPush, back: mockBack }),
}));

jest.mock('@/lib/api/incident-api', () => ({
  IncidentAPI: {
    createIncident: (...args: unknown[]) => mockCreateIncident(...args),
  },
}));

jest.mock('@/lib/api/user-api', () => ({
  UserApi: {
    getUsers: (...args: unknown[]) => mockGetUsers(...args),
  },
}));

jest.mock('@/lib/api/cmdb-api', () => ({
  CMDBApi: {
    searchCIs: (...args: unknown[]) => mockSearchCIs(...args),
  },
}));

jest.mock('@/lib/hooks/useErrorHandler', () => ({
  useErrorHandler: () => ({ handleError: mockHandleError }),
}));

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import CreateIncidentPage from '../page';

describe('CreateIncidentPage', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetUsers.mockResolvedValue({ users: [] });
    mockSearchCIs.mockResolvedValue({ items: [] });
  });

  it('reuses the same Idempotency-Key across a failed then retried submission', async () => {
    mockCreateIncident
      .mockRejectedValueOnce(new Error('network error'))
      .mockResolvedValueOnce({ id: 1 });

    render(<CreateIncidentPage />);

    fireEvent.change(screen.getByTestId('incident-title-input'), {
      target: { value: 'VPN down' },
    });
    fireEvent.change(screen.getByTestId('incident-description-input'), {
      target: { value: 'Users cannot access the VPN gateway.' },
    });

    fireEvent.click(screen.getByTestId('incident-submit-button'));
    await waitFor(() => expect(mockCreateIncident).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByTestId('incident-submit-button'));
    await waitFor(() => expect(mockCreateIncident).toHaveBeenCalledTimes(2));

    const firstKey = mockCreateIncident.mock.calls[0][1];
    const secondKey = mockCreateIncident.mock.calls[1][1];

    expect(firstKey).toBeDefined();
    expect(firstKey).toBe(secondKey);
  });
});
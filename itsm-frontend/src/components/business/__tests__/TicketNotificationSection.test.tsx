import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { TicketNotificationSection } from '../TicketNotificationSection';
import { httpClient } from '@/lib/api/http-client';

jest.mock('antd', () => {
  const actual = jest.requireActual('antd');
  return {
    ...actual,
    App: { useApp: () => ({ message: { success: jest.fn(), error: jest.fn() } }) },
  };
});

jest.mock('@/components/common/UserSelect', () => ({ UserSelect: () => null }));
jest.mock('@/lib/store/auth-store', () => ({ useAuthStore: () => ({ user: { id: 1 } }) }));
jest.mock('@/lib/i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }));
jest.mock('@/lib/api/http-client', () => ({
  httpClient: { get: jest.fn(), post: jest.fn(), put: jest.fn(), delete: jest.fn() },
}));

const mockGet = httpClient.get as jest.Mock;
const mockPut = httpClient.put as jest.Mock;

describe('TicketNotificationSection', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGet.mockResolvedValue({
      notifications: [{
        id: 5,
        ticketId: 10,
        userId: 1,
        type: 'assigned',
        channel: 'in_app',
        content: 'Assigned to you',
        status: 'sent',
        createdAt: '2026-08-31T00:00:00Z',
      }],
      total: 1,
    });
    mockPut.mockResolvedValue(undefined);
  });

  it('marks TicketNotification records through the dedicated API namespace', async () => {
    const user = userEvent.setup();
    render(<TicketNotificationSection ticketId={10} />);

    await user.click(await screen.findByRole('button', { name: '标记已读' }));

    await waitFor(() => {
      expect(mockPut).toHaveBeenCalledWith('/api/v1/ticket-notifications/5/read', {});
    });
  });
});

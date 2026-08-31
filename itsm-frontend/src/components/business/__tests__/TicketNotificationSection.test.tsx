import React from 'react';
import { render, screen, waitFor, within } from '@testing-library/react';
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

  it('derives unread rendering and count only from readAt, not delivery status', async () => {
    mockGet.mockResolvedValueOnce({
      notifications: [
        {
          id: 5,
          ticketId: 10,
          userId: 1,
          type: 'assigned',
          channel: 'in_app',
          content: 'Pending but already read',
          status: 'pending',
          readAt: '2026-08-31T01:00:00Z',
          createdAt: '2026-08-31T00:00:00Z',
        },
        {
          id: 6,
          ticketId: 10,
          userId: 1,
          type: 'assigned',
          channel: 'email',
          content: 'Delivered but still unread',
          status: 'sent',
          createdAt: '2026-08-31T00:05:00Z',
        },
      ],
      total: 2,
    });

    render(<TicketNotificationSection ticketId={10} />);

    const readItem = (await screen.findByText('Pending but already read')).closest('.ant-list-item');
    const unreadItem = screen.getByText('Delivered but still unread').closest('.ant-list-item');
    expect(readItem).not.toBeNull();
    expect(unreadItem).not.toBeNull();
    expect(within(readItem as HTMLElement).queryByRole('button', { name: '标记已读' })).not.toBeInTheDocument();
    expect(within(readItem as HTMLElement).queryByText('未读')).not.toBeInTheDocument();
    expect(within(unreadItem as HTMLElement).getByRole('button', { name: '标记已读' })).toBeInTheDocument();
    expect(within(unreadItem as HTMLElement).getByText('未读')).toBeInTheDocument();
    expect(screen.getByTitle('1')).toBeInTheDocument();
  });

  it('renders persisted readAt after marking read and again after remount', async () => {
    const persistedReadAt = '2026-08-31T02:00:00Z';
    const unreadResponse = {
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
    };
    const persistedResponse = {
      notifications: [{
        ...unreadResponse.notifications[0],
        readAt: persistedReadAt,
      }],
      total: 1,
    };
    mockGet
      .mockResolvedValueOnce(unreadResponse)
      .mockResolvedValueOnce(persistedResponse)
      .mockResolvedValueOnce(persistedResponse);

    const user = userEvent.setup();
    const view = render(<TicketNotificationSection ticketId={10} />);
    await user.click(await screen.findByRole('button', { name: '标记已读' }));

    await waitFor(() => {
      expect(screen.queryByRole('button', { name: '标记已读' })).not.toBeInTheDocument();
      expect(screen.queryByText('未读')).not.toBeInTheDocument();
    });
    expect(screen.queryByTitle('1')).not.toBeInTheDocument();
    expect(screen.getByText(/阅读时间:/)).toBeInTheDocument();

    view.unmount();
    render(<TicketNotificationSection ticketId={10} />);

    expect(await screen.findByText(/阅读时间:/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '标记已读' })).not.toBeInTheDocument();
    expect(screen.queryByText('未读')).not.toBeInTheDocument();
    expect(screen.queryByTitle('1')).not.toBeInTheDocument();
  });
});

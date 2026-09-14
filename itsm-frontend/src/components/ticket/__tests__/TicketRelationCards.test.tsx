import React from 'react';
import { useAuthStore } from '@/lib/store/auth-store';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { TicketRelationCards } from '../TicketRelationCards';
import { TicketRelationsApi } from '@/lib/api/ticket-relations-api';
import { ApiError } from '@/lib/api/http-client';

jest.mock('@/lib/api/ticket-relations-api', () => ({
  TicketRelationsApi: { getTicketRelations: jest.fn() },
}));
const list = TicketRelationsApi.getTicketRelations as jest.Mock;
const row = (title: string) => ({
  id: 'parent_child_1_2',
  sourceTicketId: 1,
  targetTicketId: 2,
  sourceTicket: { id: 1, title, status: 'open' },
  relationType: 'parent_child',
});
beforeEach(() => list.mockReset());

it('distinguishes failed reads from an empty relation list and retries', async () => {
  list.mockRejectedValueOnce(new Error('网络异常')).mockResolvedValueOnce([]);
  render(<TicketRelationCards ticketId={2} />);
  expect(await screen.findByRole('alert')).toHaveTextContent('网络异常');
  expect(screen.queryByText('暂无关联工单')).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: '重试' }));
  expect(await screen.findByText('暂无关联工单')).toBeVisible();
});

it('preserves data for a network failure but removes it after a denied refresh', async () => {
  list
    .mockResolvedValueOnce([row('敏感标题')])
    .mockRejectedValueOnce(new Error('网络异常'))
    .mockRejectedValueOnce(new ApiError('无权读取', 403));
  render(<TicketRelationCards ticketId={2} />);
  await screen.findByText(/敏感标题/);
  await userEvent.click(screen.getByRole('button', { name: '刷新' }));
  await screen.findByRole('alert');
  expect(screen.getByText(/敏感标题/)).toBeVisible();
  await userEvent.click(screen.getByRole('button', { name: '重试' }));
  await waitFor(() => expect(screen.queryByText(/敏感标题/)).not.toBeInTheDocument());
});

it('does not restore the previous ticket when an earlier response arrives late', async () => {
  let finish!: (data: unknown) => void;
  list
    .mockImplementationOnce(
      () =>
        new Promise(resolve => {
          finish = resolve;
        })
    )
    .mockResolvedValueOnce([]);
  const { rerender } = render(<TicketRelationCards ticketId={2} />);
  rerender(<TicketRelationCards ticketId={3} />);
  await screen.findByText('暂无关联工单');
  await act(async () => finish([row('旧标题')]));
  expect(screen.queryByText(/旧标题/)).not.toBeInTheDocument();
});

it('clears the visible list and count when the tenant changes', async () => {

  const original = useAuthStore.getState();
  const onCountChange = jest.fn();
  list.mockResolvedValueOnce([row('原租户内容')]).mockResolvedValueOnce([]);
  const view = render(<TicketRelationCards ticketId={2} onCountChange={onCountChange} />);
  await screen.findByText(/原租户内容/);
  expect(onCountChange).toHaveBeenLastCalledWith(1);
  act(() =>
    useAuthStore.setState({
      currentTenant: { id: 99 } as NonNullable<typeof original.currentTenant>,
    })
  );
  expect(screen.queryByText(/原租户内容/)).not.toBeInTheDocument();
  await screen.findByText('暂无关联工单');
  expect(onCountChange).toHaveBeenLastCalledWith(0);
  view.unmount();
  act(() => useAuthStore.setState(original));
});

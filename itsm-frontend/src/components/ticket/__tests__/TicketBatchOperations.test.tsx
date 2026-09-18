import React from 'react';
import { render, screen, within, waitFor } from '@testing-library/react';
import userEvent, { PointerEventsCheckLevel } from '@testing-library/user-event';
import TicketBatchOperations from '../TicketBatchOperations';
import { TicketAPI } from '@/lib/api/ticket-api';
import type { Ticket } from '@/lib/api/types';

jest.mock('@/lib/api/ticket-api', () => ({ TicketAPI: { updateTicket: jest.fn() } }));

const ticket = { id: 101, ticketNumber: 'EDIT-101', title: 'Observed work', status: 'open', priority: 'medium', version: 4 } as Ticket;

describe('TicketBatchOperations edit menu', () => {
  it.each([
    ['批量更新状态', '新状态'],
    ['批量设置优先级', '优先级'],
    ['批量添加标签', '标签'],
  ])('opens the actual %s form', async (menu, field) => {
    const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
    render(<TicketBatchOperations selectedTickets={[ticket]} />);
    await user.hover(screen.getByText('批量操作'));
    await user.click(await screen.findByText(menu));
    const dialog = (await screen.findAllByText(menu)).map(node => node.closest('[role="dialog"]')).find(Boolean) as HTMLElement;
    expect(dialog).not.toBeNull();
    expect(within(dialog).getByLabelText(field)).toBeInTheDocument();
  });
});


describe('TicketBatchOperations confirmed edit retries', () => {
  const update = TicketAPI.updateTicket as jest.Mock;
  const originalRandomUUID = crypto.randomUUID;
  beforeEach(() => {
    update.mockReset();
    let sequence = 0;
    Object.defineProperty(crypto, 'randomUUID', {
      configurable: true,
      value: jest.fn(() => `batch-edit-${++sequence}`),
    });
  });
  afterEach(() => {
    Object.defineProperty(crypto, 'randomUUID', { configurable: true, value: originalRandomUUID });
  });

  it.each([
    ['批量更新状态', '新状态', '处理中', { status: 'in_progress' }],
    ['批量设置优先级', '优先级', '高', { priority: 'high' }],
  ])('reuses the uncertain request from the actual %s form', async (menu, field, option, fields) => {
    const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
    const complete = jest.fn();
    update.mockRejectedValueOnce(new Error('connection lost')).mockResolvedValueOnce({
      workItemId: 101, version: 5, status: 'in_progress', replayed: true,
    });
    const { rerender } = render(<TicketBatchOperations selectedTickets={[ticket]} onOperationComplete={complete} />);
    await user.hover(screen.getByText('批量操作'));
    await user.click(await screen.findByText(menu as string));
    const dialog = screen.getAllByText(menu as string).map(node => node.closest('[role="dialog"]')).find(Boolean) as HTMLElement;
    await user.click(within(dialog).getByLabelText(field as string));
    await user.click(await screen.findByText(option as string));
    await user.click(within(dialog).getByText('确认操作'));
    await waitFor(() => expect(complete).toHaveBeenCalledTimes(1), { timeout: 3000 });
    expect(update).toHaveBeenNthCalledWith(1, 101, { ...fields as object, version: 4, operationId: 'batch-edit-1' });
    rerender(<TicketBatchOperations selectedTickets={[{ ...ticket, version: 9 }]} onOperationComplete={complete} />);
    await user.click(within(dialog).getByText('确认操作'));
    await waitFor(() => expect(update).toHaveBeenCalledTimes(2));
    expect(update.mock.calls[1]).toEqual(update.mock.calls[0]);
    expect(crypto.randomUUID).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(complete).toHaveBeenCalledTimes(2), { timeout: 3000 });
  });

  it('uses the refreshed version and a new operation after explicit conflict and confirmation', async () => {
    const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
    const complete = jest.fn();
    update.mockRejectedValueOnce(Object.assign(new Error('version conflict'), { status: 409, code: 4090 }))
      .mockResolvedValueOnce({ workItemId: 101, version: 10, status: 'open', replayed: false });
    const { rerender } = render(<TicketBatchOperations selectedTickets={[ticket]} onOperationComplete={complete} />);
    await user.hover(screen.getByText('批量操作'));
    await user.click(await screen.findByText('批量设置优先级'));
    const dialog = screen.getAllByText('批量设置优先级').map(node => node.closest('[role="dialog"]')).find(Boolean) as HTMLElement;
    await user.click(within(dialog).getByLabelText('优先级'));
    await user.click(await screen.findByText('高'));
    await user.click(within(dialog).getByText('确认操作'));
    await waitFor(() => expect(complete).toHaveBeenCalledTimes(1), { timeout: 3000 });
    expect(update).toHaveBeenCalledTimes(1);
    rerender(<TicketBatchOperations selectedTickets={[{ ...ticket, version: 9 }]} onOperationComplete={complete} />);
    expect(update).toHaveBeenCalledTimes(1);
    await user.click(within(dialog).getByText('确认操作'));
    await waitFor(() => expect(update).toHaveBeenCalledTimes(2));
    expect(update).toHaveBeenNthCalledWith(1, 101, { priority: 'high', version: 4, operationId: 'batch-edit-1' });
    expect(update).toHaveBeenNthCalledWith(2, 101, { priority: 'high', version: 9, operationId: 'batch-edit-2' });
    await waitFor(() => expect(complete).toHaveBeenCalledTimes(2), { timeout: 3000 });
  });
});

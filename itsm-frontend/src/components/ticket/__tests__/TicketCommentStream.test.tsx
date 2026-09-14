import React from 'react';
import { App } from 'antd';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { TicketCommentStream } from '../TicketCommentStream';
import { ticketCommentAdapter } from '@/components/business/detail-tabs';
import { ApiError } from '@/lib/api/http-client';
jest.mock('@/components/common/UserSelect', () => ({ UserSelect: () => null }));
jest.mock('@/components/business/detail-tabs', () => ({
  ticketCommentAdapter: {
    list: jest.fn(),
    create: jest.fn(),
    update: jest.fn(),
    remove: jest.fn(),
  },
}));
const list = ticketCommentAdapter.list as jest.Mock;
const entry = { id: 1, userId: 7, content: '现有内容', createdAt: '2026-09-14' };
beforeEach(() => {
  list.mockReset();
  list.mockResolvedValue({ comments: [entry], total: 1 });
});

it('clears sensitive content and drafts when refresh is forbidden', async () => {
  render(
    <App>
      <TicketCommentStream ticketId={101} currentUserId={7} />
    </App>
  );
  const user = userEvent.setup();
  await screen.findByText('现有内容');
  await user.type(screen.getByRole('textbox'), '未发送草稿');
  list.mockRejectedValueOnce(new ApiError('权限撤销', 403));
  await user.click(screen.getByRole('button', { name: '刷新' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('权限撤销');
  expect(screen.queryByText('现有内容')).not.toBeInTheDocument();
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: '重试' }));
  expect(await screen.findByRole('textbox')).toHaveValue('');
});

it('does not carry drafts or a late write result to another ticket', async () => {
  let finish!: () => void;
  (ticketCommentAdapter.create as jest.Mock).mockImplementationOnce(
    () =>
      new Promise(resolve => {
        finish = () => resolve(entry);
      })
  );
  const { rerender } = render(
    <App>
      <TicketCommentStream ticketId={101} />
    </App>
  );
  const user = userEvent.setup();
  await screen.findByText('现有内容');
  await user.type(screen.getByRole('textbox'), '只属于101');
  await user.click(screen.getByRole('button', { name: /发送评论/ }));
  rerender(
    <App>
      <TicketCommentStream ticketId={202} />
    </App>
  );
  expect(await screen.findByRole('textbox')).toHaveValue('');
  await user.type(screen.getByRole('textbox'), '202草稿');
  await act(async () => finish());
  expect(screen.getByRole('textbox')).toHaveValue('202草稿');
  expect(list).toHaveBeenLastCalledWith(202);
});

it('updates the parent count from refreshed comments after posting', async () => {
  const onCountChange = jest.fn();
  (ticketCommentAdapter.create as jest.Mock).mockResolvedValue(entry);
  render(
    <App>
      <TicketCommentStream ticketId={101} onCountChange={onCountChange} />
    </App>
  );
  await screen.findByText('现有内容');
  list.mockResolvedValueOnce({
    comments: [entry, { ...entry, id: 2, content: '新评论' }],
    total: 2,
  });
  const user = userEvent.setup();
  await user.type(screen.getByRole('textbox'), '新评论');
  await user.click(screen.getByRole('button', { name: /发送评论/ }));
  await waitFor(() => expect(onCountChange).toHaveBeenLastCalledWith(2));
  expect(screen.getByRole('textbox')).toHaveValue('');
});

it('ignores a late successful write after the same ticket loses authorization', async () => {
  let finish!: () => void;
  (ticketCommentAdapter.create as jest.Mock).mockImplementationOnce(() => new Promise(resolve => { finish = () => resolve(entry); }));
  const onCountChange = jest.fn();
  render(<App><TicketCommentStream ticketId={101} onCountChange={onCountChange} /></App>);
  await screen.findByText('现有内容');
  await userEvent.type(screen.getByRole('textbox'), '等待发布');
  await userEvent.click(screen.getByRole('button', { name: /发送评论/ }));
  list.mockRejectedValueOnce(new ApiError('会话失效', 401));
  await userEvent.click(screen.getByRole('button', { name: '刷新' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('会话失效');
  expect(onCountChange).toHaveBeenLastCalledWith(undefined);
  const reads = list.mock.calls.length;
  await act(async () => finish());
  expect(list).toHaveBeenCalledTimes(reads);
  expect(screen.queryByText('现有内容')).not.toBeInTheDocument();
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
});
import React from 'react';
import { App } from 'antd';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { CommentPanel } from '../CommentPanel';
import type { CommentAdapter } from '../types';
import { ApiError } from '@/lib/api/http-client';
it('removes shared comments and draft after a final unauthorized read', async () => {
  const adapter = { list: jest.fn().mockResolvedValueOnce({ comments: [{ id: 1, userId: 7, content: '共享评论', createdAt: '' }], total: 1 }).mockRejectedValueOnce(new ApiError('登录失效', 401)), create: jest.fn(), remove: jest.fn() };
  render(<App><CommentPanel targetType="ticket" targetId={101} adapter={adapter} showMentions={false} /></App>);
  await screen.findByText('共享评论');
  await userEvent.type(screen.getByRole('textbox'), '私密草稿');
  await userEvent.click(screen.getByRole('button', { name: '刷新' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('登录失效');
  expect(screen.queryByText('共享评论')).not.toBeInTheDocument();
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
});
it('keeps a newly posted comment when an earlier background read arrives late', async () => {
  const original = { id: 1, userId: 7, content: 'original comment', createdAt: '2026-09-14' };
  const written = { ...original, id: 2, content: 'persisted comment' };
  const list = jest.fn().mockResolvedValue({ comments: [original], total: 1 });
  const adapter: CommentAdapter = { list, create: jest.fn().mockResolvedValue(written), remove: jest.fn() };
  render(<App><CommentPanel targetType='ticket' targetId={101} adapter={adapter} showMentions={false} /></App>);
  const user = userEvent.setup();
  await screen.findByText('original comment');
  let finish!: (value: { comments: typeof original[]; total: number }) => void;
  list.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
  await user.click(screen.getByRole('button', { name: '刷新' }));
  list.mockResolvedValue({ comments: [original, written], total: 2 });
  await user.type(screen.getByRole('textbox'), 'persisted comment');
  await user.click(screen.getByRole('button', { name: /发送评论/ }));
  try { await waitFor(() => expect(list).toHaveBeenCalledTimes(3)); }
  finally { await act(async () => finish({ comments: [original], total: 1 })); }
  expect(screen.getByText('persisted comment', { exact: true })).toBeVisible();
});

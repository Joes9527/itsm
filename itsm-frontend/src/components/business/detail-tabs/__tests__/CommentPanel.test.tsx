import React from 'react';
import { App } from 'antd';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { CommentPanel } from '../CommentPanel';
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
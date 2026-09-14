import React from 'react';
import { App } from 'antd';
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent, { PointerEventsCheckLevel } from '@testing-library/user-event';
import WorkspaceTicketsPage from '../page';
import { useAuthStore } from '@/lib/store/auth-store';
import { httpClient } from '@/lib/api/http-client';
import type { SessionUser, Tenant } from '@/lib/api/api-config';

jest.mock('next/navigation', () => ({
  useParams: () => ({}),
  useRouter: () => ({ push: jest.fn() }),
}));
jest.mock('@/lib/api/http-client', () => ({
  ...jest.requireActual('@/lib/api/http-client'),
  httpClient: { get: jest.fn(), post: jest.fn(), put: jest.fn(), delete: jest.fn() },
}));
jest.mock('@/components/business/AISuggestionPanel', () => ({ AISuggestionPanel: () => null }));
jest.mock('@/components/ticket/KBRecommendCard', () => ({ KBRecommendCard: () => null }));
jest.mock('@/components/ticket/CIContextCard', () => ({ CIContextCard: () => null }));
jest.mock('@/components/workspace/AISimilarSolutionsPanel', () => ({
  AISimilarSolutionsPanel: () => null,
}));
const get = httpClient.get as jest.Mock;
const post = httpClient.post as jest.Mock;
const originalAuth = useAuthStore.getState();
const ticket = (id: number) => ({
  id,
  ticketNumber: `TKT-${id}`,
  title: `真实工单 ${id}`,
  description: `描述 ${id}`,
  recordClass: 'generic',
  status: 'open',
  priority: 'low',
  version: 1,
  createdAt: '2026-09-14T00:00:00Z',
  updatedAt: '2026-09-14T00:00:00Z',
  assigneeId: 7,
  requesterId: 8,
  assignee: { id: 7, name: 'Engineer' },
  requester: { id: 8, name: 'Requester' },
  actions: {},
});
function setupGet(url: string) {
  if (url === '/api/v1/tickets')
    return Promise.resolve({
      tickets: [ticket(101), ticket(102)],
      total: 2,
      page: 1,
      pageSize: 20,
    });
  if (/\/tickets\/\d+$/.test(url)) return Promise.resolve(ticket(Number(url.split('/').pop())));
  if (url.endsWith('/comments'))
    return Promise.resolve({
      comments: [
        {
          id: 9,
          content: `已保存评论 ${url.includes('/101/') ? '101' : '102'}`,
          createdAt: '2026-09-14T00:00:00Z',
          userId: 8,
        },
      ],
      total: 1,
    });
  if (url.endsWith('/attachments')) return Promise.resolve({ attachments: [], total: 0 });
  if (url.endsWith('/sla')) return Promise.resolve(null);
  if (url.endsWith('/relations')) return Promise.resolve({ relations: [], total: 0 });
  return Promise.resolve([]);
}
beforeEach(() => {
  jest.clearAllMocks();
  useAuthStore.setState({
    user: { id: 7, tenantId: 1, actorTenantId: 1, permissions: ['ticket:read'] } as SessionUser,
    currentTenant: { id: 1, status: 'active' } as Tenant,
    isAuthenticated: true,
  });
  get.mockImplementation(setupGet);
});
afterEach(() => act(() => useAuthStore.setState(originalAuth)));
const user = () => userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });

it('loads real tickets, keeps unsupported queues disabled, and uses the selected WorkItem for real comments', async () => {
  render(
    <App>
      <WorkspaceTicketsPage />
    </App>
  );
  const second = await screen.findByRole('button', { name: /TKT-102/ });
  expect(get).toHaveBeenCalledWith('/api/v1/tickets', { assigneeId: 7, page: 1, pageSize: 20 });
  expect(
    within(screen.getByLabelText('个人工单队列')).getByRole('button', { name: /未分配池/ })
  ).toBeDisabled();
  expect(
    within(screen.getByLabelText('个人工单队列')).getByRole('button', { name: /即将超时/ })
  ).toBeDisabled();
  expect(screen.queryByText('完成并结单')).not.toBeInTheDocument();
  await screen.findByText('已保存评论 101');
  await user().type(screen.getByPlaceholderText('输入您的评论或内部评估记录...'), 'A 的草稿');
  await user().click(second);
  await screen.findByText('已保存评论 102');
  expect(screen.queryByText('已保存评论 101')).not.toBeInTheDocument();
  expect(screen.getByPlaceholderText('输入您的评论或内部评估记录...')).toHaveValue('');
  expect(get).toHaveBeenCalledWith('/api/v1/tickets/102/comments');
  expect(post).not.toHaveBeenCalled();
});
it('unmounts real detail content and drafts when permissions are withdrawn', async () => {
  render(
    <App>
      <WorkspaceTicketsPage />
    </App>
  );
  await screen.findByText('已保存评论 101');
  act(() => useAuthStore.getState().updateUser({ permissions: [] }));
  await waitFor(() => expect(screen.queryByText('已保存评论 101')).not.toBeInTheDocument());
  expect(screen.queryByPlaceholderText('输入您的评论或内部评估记录...')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /TKT-101/ })).not.toBeInTheDocument();
});
it('shows a retryable queue error and a successful empty state separately', async () => {
  get.mockRejectedValueOnce(new Error('queue unavailable'));
  render(
    <App>
      <WorkspaceTicketsPage />
    </App>
  );
  await screen.findByText(/queue unavailable/);
  expect(screen.queryByText('暂无分配给您的工单')).not.toBeInTheDocument();
  get.mockImplementation(async (url: string) =>
    url === '/api/v1/tickets' ? { tickets: [], total: 0, page: 1, pageSize: 20 } : setupGet(url)
  );
  await user().click(screen.getByRole('button', { name: '重试队列' }));
  expect(await screen.findByText('暂无分配给您的工单')).toBeVisible();
});

it('ignores a late comment response from the previously selected ticket', async () => {
  let finish!: (value: unknown) => void;
  get.mockImplementation((url: string) =>
    url === '/api/v1/tickets/101/comments'
      ? new Promise(resolve => {
          finish = resolve;
        })
      : setupGet(url)
  );
  render(
    <App>
      <WorkspaceTicketsPage />
    </App>
  );
  await waitFor(() => expect(get).toHaveBeenCalledWith('/api/v1/tickets/101/comments'));
  await user().click(
    within(screen.getByLabelText('个人工单队列')).getByRole('button', { name: /TKT-102/ })
  );
  await screen.findByText('已保存评论 102');
  await act(async () =>
    finish({
      comments: [{ id: 20, content: '迟到的 A 内容', createdAt: '2026-09-14T00:00:00Z' }],
      total: 1,
    })
  );
  expect(screen.queryByText('迟到的 A 内容')).not.toBeInTheDocument();
  expect(screen.getByText('已保存评论 102')).toBeInTheDocument();
});

import React from 'react';
import { App } from 'antd';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { WorkItemShell } from '../WorkItemShell';
import type { WorkItemCommon } from '../WorkItemTypes';
import { httpClient } from '@/lib/api/http-client';
import { useAuthStore } from '@/lib/store/auth-store';
jest.mock('@/lib/api/http-client', () => ({
  ...jest.requireActual('@/lib/api/http-client'),
  httpClient: { get: jest.fn(), post: jest.fn(), delete: jest.fn() },
}));
const originalAuth = useAuthStore.getState();
const get = httpClient.get as jest.Mock;
const attachment = {
  id: 17,
  fileName: 'shared-proof.txt',
  fileSize: 3,
  mimeType: 'text/plain',
  createdAt: '',
};
const item: WorkItemCommon = {
  id: 101,
  number: 'WI-101',
  title: '共享工作项',
  recordClass: 'incident',
  status: 'open',
  priority: 'high',
  requesterId: 7,
  createdAt: '',
  updatedAt: '',
};
beforeEach(() => {
  useAuthStore.setState({ hasPermission: permission => permission !== 'user:read' });
  get.mockImplementation(async (url: string) => {
    if (url.endsWith('/preview')) return { type: 'text/plain', text: async () => 'shared preview proof' };
    if (url.endsWith('/attachments')) return { attachments: [attachment], total: 1 };
    if (url.endsWith('/comments'))
      return { comments: [{ id: 9, content: '共享正文', createdAt: '' }], total: 1 };
    return [];
  });
});
afterEach(() => act(() => useAuthStore.setState(originalAuth)));

it.each(['generic', 'incident', 'problem', 'change_request'] as const)(
  'uses the WorkItem identity through real %s panels and API clients',
  async recordClass => {
    const professionalExtension = { id: 999, workItemId: 101 };
    render(
      <App>
        <WorkItemShell
          workItem={{ ...item, recordClass }}
          actions={{}}
          onActionDispatch={jest.fn()}
          professionalPanelSlot={<div>扩展 #{professionalExtension.id}</div>}
        />
      </App>
    );
    await screen.findByText('shared-proof.txt');
    await screen.findByText('共享正文');
    expect(get).toHaveBeenCalledWith('/api/v1/tickets/101/attachments');
    expect(get).toHaveBeenCalledWith('/api/v1/tickets/101/comments');
    expect(get).toHaveBeenCalledWith('/api/v1/tickets/101/relations', expect.anything());
    expect(get.mock.calls.some(([url]) => String(url).includes('/999/'))).toBe(false);
    await userEvent.click(screen.getByRole('button', { name: '预览' }));
    await waitFor(() => expect(screen.getByText('shared preview proof')).toBeVisible());
    expect(get).toHaveBeenCalledWith('/api/v1/tickets/101/attachments/17/preview', undefined, { responseType: 'blob', assertSubmissionContext: expect.any(Function) });
  }
);

it('clears real shared panel content on tenant switch and refuses denied reloads', async () => {
  render(
    <App>
      <WorkItemShell
        workItem={item}
        actions={{}}
        onActionDispatch={jest.fn()}
        professionalPanelSlot={null}
      />
    </App>
  );
  await screen.findByText('shared-proof.txt');
  await userEvent.type(screen.getByRole('textbox'), '租户草稿');
  const { ApiError } = jest.requireActual('@/lib/api/http-client');
  get.mockRejectedValue(new ApiError('租户权限拒绝', 403));
  act(() =>
    useAuthStore.setState({
      currentTenant: { id: 22 } as NonNullable<typeof originalAuth.currentTenant>,
    })
  );
  await waitFor(() => expect(screen.queryByText('shared-proof.txt')).not.toBeInTheDocument());
  expect(screen.queryByText('共享正文')).not.toBeInTheDocument();
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '上传附件' })).not.toBeInTheDocument();
});
it('withdraws shared attachment controls after same-session permissions change', async () => {
  useAuthStore.setState({
    hasPermission: originalAuth.hasPermission,
    user: {
      id: 7,
      username: 'engineer',
      name: 'Engineer',
      email: '',
      role: 'technician',
      tenantId: 1,
      actorTenantId: 1,
      permissions: ['incident:read', 'incident:create', 'incident:delete'],
    },
  });
  render(
    <App>
      <WorkItemShell
        workItem={item}
        actions={{}}
        onActionDispatch={jest.fn()}
        professionalPanelSlot={null}
      />
    </App>
  );
  await screen.findByText('shared-proof.txt');
  act(() => useAuthStore.getState().updateUser({ permissions: [] }));
  await waitFor(() => expect(screen.queryByText('shared-proof.txt')).not.toBeInTheDocument());
  expect(screen.queryByRole('button', { name: '上传附件' })).not.toBeInTheDocument();
});

import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { WorkItemRelations } from '../WorkItemRelations';
import { WorkItemRelationsApi } from '@/lib/api/workitem-relations';
import { useAuthStore } from '@/lib/store/auth-store';
jest.mock('@/lib/api/workitem-relations', () => ({
  WorkItemRelationsApi: { list: jest.fn(), context: jest.fn(), add: jest.fn(), remove: jest.fn() },
}));
jest.mock('@/lib/store/auth-store', () => ({ useAuthStore: { getState: jest.fn() } }));
const source = {
  workItemId: 101,
  number: 'INC-101',
  recordClass: 'incident',
  title: 'Source',
  status: 'new',
  version: 7,
};
const target = { ...source, workItemId: 202, number: 'PRB-202', recordClass: 'problem' };
beforeEach(() => {
  Object.defineProperty(global.crypto, 'randomUUID', {
    configurable: true,
    value: jest.fn().mockReturnValueOnce('first-operation').mockReturnValueOnce('next-operation'),
  });
  jest.clearAllMocks();
  (useAuthStore.getState as jest.Mock).mockReturnValue({
    user: { id: 1 },
    currentTenant: { id: 2 },
    isAuthenticated: true,
  });
  (WorkItemRelationsApi.context as jest.Mock).mockImplementation(async (id: number) => ({
    source: id === 101 ? source : target,
    mutation: { allowed: true },
  }));
  (WorkItemRelationsApi.list as jest.Mock).mockResolvedValue([]);
});
it('shows canonical direction and preserves successful receipt when refresh is denied', async () => {
  (WorkItemRelationsApi.list as jest.Mock)
    .mockResolvedValueOnce([
      { id: 9, source, target, relationType: 'investigated_by', required: false },
    ])
    .mockRejectedValue(new Error('权限已撤销'));
  (WorkItemRelationsApi.remove as jest.Mock).mockResolvedValue({
    workItemId: 101,
    version: 8,
    status: 'new',
    replayed: false,
  });
  render(<WorkItemRelations workItemId={101} />);
  expect(await screen.findByText(/INC-101.*→.*PRB-202/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '准备解除关联' }));
  fireEvent.click(await screen.findByRole('button', { name: '确认解除关联' }));
  expect(await screen.findByText(/解除关联已确认/)).toBeInTheDocument();
  expect(await screen.findByText(/刷新失败.*权限已撤销/)).toBeInTheDocument();
  expect(WorkItemRelationsApi.remove).toHaveBeenCalledWith(
    expect.objectContaining({ sourceWorkItemId: 101, targetWorkItemId: 202, expectedVersion: 7 }),
    expect.any(Function)
  );
});
it('retries frozen payload and version after uncertain outcome; explicit refresh starts new confirmation', async () => {
  (WorkItemRelationsApi.add as jest.Mock).mockRejectedValue(new Error('network lost'));
  render(<WorkItemRelations workItemId={101} />);
  await screen.findByText(/INC-101/);
  fireEvent.change(screen.getByLabelText('目标 WorkItem ID'), { target: { value: '202' } });
  fireEvent.click(screen.getByRole('button', { name: '确认关联' }));
  await screen.findByText(/network lost/);
  const first = (WorkItemRelationsApi.add as jest.Mock).mock.calls[0][0];
  fireEvent.change(screen.getByLabelText('目标 WorkItem ID'), { target: { value: '303' } });
  fireEvent.click(screen.getByRole('button', { name: '重试原操作' }));
  await waitFor(() => expect(WorkItemRelationsApi.add).toHaveBeenCalledTimes(2));
  expect((WorkItemRelationsApi.add as jest.Mock).mock.calls[1][0]).toEqual(first);
  (WorkItemRelationsApi.context as jest.Mock).mockResolvedValue({
    source: { ...source, version: 8 },
    mutation: { allowed: true },
  });
  fireEvent.click(screen.getByRole('button', { name: '刷新并重新确认' }));
  await screen.findByText(/版本 8/);
  expect(screen.getByRole('button', { name: /重试先前操作 first-operation/ })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '确认关联' }));
  await waitFor(() => expect(WorkItemRelationsApi.add).toHaveBeenCalledTimes(3));
  const next = (WorkItemRelationsApi.add as jest.Mock).mock.calls[2][0];
  expect(next.targetWorkItemId).toBe(303);
  expect(next.expectedVersion).toBe(8);
  expect(next.operationId).not.toBe(first.operationId);
});
it('read denial is visible and cannot be mistaken for empty relations', async () => {
  (WorkItemRelationsApi.list as jest.Mock).mockRejectedValue(new Error('不可读取关联'));
  render(<WorkItemRelations workItemId={101} />);
  expect(await screen.findByText(/不可读取关联/)).toBeInTheDocument();
  expect(screen.queryByText('暂无关联')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: '确认关联' })).toBeDisabled();
});

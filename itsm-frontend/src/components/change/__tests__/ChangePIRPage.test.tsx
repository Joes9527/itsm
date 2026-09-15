import React from 'react';
import { render, screen, waitFor, within } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import { ChangeApi } from '@/lib/api/change-api';
import PIRPage from '@/app/(main)/changes/[id]/pir/page';
import PIRListPage from '@/app/(main)/changes/pirs/page';
jest.unmock('dayjs');

jest.mock('next/navigation', () => ({
  useParams: () => ({ id: '1' }),
  useRouter: () => ({ push: jest.fn() }),
}));
jest.mock('@/lib/i18n/useI18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }));
jest.mock('@/lib/api/change-api', () => ({
  ChangeApi: {
    getChange: jest.fn(),
    getPIR: jest.fn(),
    createPIR: jest.fn(),
    updatePIR: jest.fn(),
    getPIRs: jest.fn(),
    deletePIR: jest.fn(),
  },
}));

beforeEach(() => {
  jest.clearAllMocks();
  Object.defineProperty(global.crypto, 'randomUUID', {
    configurable: true,
    value: () => 'pir-operation',
  });
});

test('list delete confirms the observed PIR/version and never replaces it at submission', async () => {
  const user = userEvent.setup();
  const pir = { id: 4, changeId: 1, changeTitle: 'Observed change', overallResult: 'failed', reviewDate: '2026-09-10T01:00:00Z' } as any;
  jest.mocked(ChangeApi.getPIRs).mockResolvedValue({ total: 1, items: [pir] });
  jest.mocked(ChangeApi.getPIR).mockResolvedValue(pir);
  jest.mocked(ChangeApi.getChange).mockResolvedValue({ id: 1, version: 7 } as any);
  jest.mocked(ChangeApi.deletePIR).mockResolvedValue({ workItemId: 31, version: 8, status: 'in_progress', replayed: false, pirId: 4 });
  render(<PIRListPage />);
  await user.click(await screen.findByRole('button', { name: /^删\s*除$/ }));
  expect(await screen.findByText('PIR 4 · 变更版本 7 · 结果 failed')).toBeInTheDocument();
  expect(ChangeApi.deletePIR).not.toHaveBeenCalled();
  jest.mocked(ChangeApi.getChange).mockResolvedValue({ id: 1, version: 99 } as any);
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /^删\s*除$/ }));
  await waitFor(() => expect(ChangeApi.deletePIR).toHaveBeenCalledWith(4, { changeId: 1, expectedVersion: 7, operationId: 'pir-operation' }));
  expect(ChangeApi.getChange).toHaveBeenCalledTimes(1);
});

test.each(['fresh confirmation', 'same confirmation retry'] as const)(
  'failed PIR delete preserves the correct operation boundary: %s',
  async mode => {
    const user = userEvent.setup();
    let key = 0;
    Object.defineProperty(global.crypto, 'randomUUID', {
      configurable: true, value: () => `delete-operation-${++key}`,
    });
    const pir = { id: 4, changeId: 1, changeTitle: 'Observed change', overallResult: 'failed', reviewDate: '2026-09-10T01:00:00Z' } as any;
    jest.mocked(ChangeApi.getPIRs).mockResolvedValue({ total: 1, items: [pir] });
    jest.mocked(ChangeApi.getPIR).mockResolvedValue(pir);
    jest.mocked(ChangeApi.getChange).mockResolvedValueOnce({ id: 1, version: 7 } as any).mockResolvedValue({ id: 1, version: 8 } as any);
    const errorText = mode === 'fresh confirmation' ? 'version conflict' : 'network response lost';
    jest.mocked(ChangeApi.deletePIR).mockRejectedValueOnce(new Error(errorText)).mockResolvedValue({ workItemId: 31, version: 9, status: 'in_progress', replayed: false, pirId: 4 });
    render(<PIRListPage />);
    await user.click(await screen.findByRole('button', { name: /^删\s*除$/ }));
    await screen.findByText('PIR 4 · 变更版本 7 · 结果 failed');
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /^删\s*除$/ }));
    await screen.findByText(errorText);
    if (mode === 'fresh confirmation') {
      await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /^取\s*消$/ }));
      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
      await user.click(screen.getByRole('button', { name: /^删\s*除$/ }));
      await screen.findByText('PIR 4 · 变更版本 8 · 结果 failed');
    }
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /^删\s*除$/ }));
    await waitFor(() => expect(ChangeApi.deletePIR).toHaveBeenCalledTimes(2));
    expect(jest.mocked(ChangeApi.deletePIR).mock.calls.map(([, body]) => body)).toEqual([
      { changeId: 1, expectedVersion: 7, operationId: 'delete-operation-1' },
      { changeId: 1, expectedVersion: mode === 'fresh confirmation' ? 8 : 7, operationId: mode === 'fresh confirmation' ? 'delete-operation-2' : 'delete-operation-1' },
    ]);
    expect(ChangeApi.getChange).toHaveBeenCalledTimes(mode === 'fresh confirmation' ? 2 : 1);
  }
);

test('PIR creation refetches actual detail after immutable receipt; later edit uses refreshed version', async () => {
  const user = userEvent.setup();
  jest
    .mocked(ChangeApi.getChange)
    .mockResolvedValueOnce({ id: 1, version: 7, outcome: 'failed' } as any)
    .mockResolvedValue({ id: 1, version: 8, outcome: 'failed' } as any);
  jest
    .mocked(ChangeApi.getPIR)
    .mockResolvedValueOnce(null)
    .mockResolvedValue({
      id: 4,
      changeId: 1,
      overallResult: 'failed',
      objectivesAchieved: false,
      rollbackPerformed: false,
      reviewerName: 'Actual reviewer',
      reviewDate: '2026-09-10T01:00:00Z',
    } as any);
  jest
    .mocked(ChangeApi.createPIR)
    .mockResolvedValue({
      workItemId: 31,
      version: 8,
      status: 'in_progress',
      replayed: false,
      pirId: 4,
    });
  jest
    .mocked(ChangeApi.updatePIR)
    .mockResolvedValue({
      workItemId: 31,
      version: 9,
      status: 'in_progress',
      replayed: false,
      pirId: 4,
    });
  render(<PIRPage />);
  await screen.findByText('当前变更版本 7；实施结果：failed');
  await user.click(screen.getByLabelText('总体结果'));
  await user.click(screen.getByText('失败 - 变更未能达到预期目标或需要回滚'));
  // Target the visible action first: a page-wide role/name query computes styles for every button.
  const createButton = screen.getByText('创建 PIR').closest('button')!;
  expect(createButton).toHaveAccessibleName('创建 PIR');
  expect(createButton).toBeVisible();
  expect(createButton).toBeEnabled();
  await user.click(createButton);
  expect(await screen.findByText('Actual reviewer')).toBeInTheDocument();
  expect(ChangeApi.getPIR).toHaveBeenCalledTimes(2);
  expect(ChangeApi.getChange).toHaveBeenCalledTimes(2);
  expect(screen.getByText('当前变更版本 8；实施结果：failed')).toBeInTheDocument();
  expect(ChangeApi.createPIR).toHaveBeenCalledWith(
    1,
    expect.objectContaining({
      expectedVersion: 7,
      operationId: 'pir-operation',
      overallResult: 'failed',
    })
  );
  const updateButton = screen.getByText('更新 PIR').closest('button')!;
  expect(updateButton).toHaveAccessibleName('更新 PIR');
  expect(updateButton).toBeVisible();
  expect(updateButton).toBeEnabled();
  await user.click(updateButton);
  await waitFor(() =>
    expect(ChangeApi.updatePIR).toHaveBeenCalledWith(
      4,
      expect.objectContaining({ changeId: 1, expectedVersion: 8, operationId: 'pir-operation' })
    )
  );
});

test('PIR read failure remains visible and prevents submission from an unknown version', async () => {
  jest.mocked(ChangeApi.getChange).mockRejectedValue(new Error('permission denied'));
  jest.mocked(ChangeApi.getPIR).mockResolvedValue(null);
  render(<PIRPage />);
  expect(await screen.findByText('permission denied')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '创建 PIR' })).toBeDisabled();
  expect(ChangeApi.createPIR).not.toHaveBeenCalled();
});

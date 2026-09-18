import React, { useRef, useState } from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import {
  CreationSourceRelations,
  type CreationSourceRelationsHandle,
} from '../CreationSourceRelations';
import { WorkItemRelationsApi, type SourceRelation } from '@/lib/api/workitem-relations';
jest.mock('@/lib/api/workitem-relations', () => ({ WorkItemRelationsApi: { context: jest.fn() } }));
function FormFixture() {
  const [value, setValue] = useState<SourceRelation[]>([]);
  const ref = useRef<CreationSourceRelationsHandle>(null);
  return (
    <>
      <CreationSourceRelations ref={ref} value={value} onChange={setValue} />
      <output aria-label='wire'>{JSON.stringify(value)}</output>
    </>
  );
}
beforeEach(() => jest.clearAllMocks());
it('binds observed WorkItem identity/version and typed required metadata, never professional ID', async () => {
  (WorkItemRelationsApi.context as jest.Mock).mockResolvedValue({
    source: { workItemId: 101, number: 'INC-REAL', version: 7 },
    mutation: { allowed: true },
  });
  render(<FormFixture />);
  fireEvent.change(screen.getByLabelText('源 WorkItem ID'), { target: { value: '101' } });
  fireEvent.click(screen.getByRole('checkbox', { name: '必须验证的变更依赖' }));
  fireEvent.click(screen.getByRole('button', { name: '读取并添加源记录' }));
  await waitFor(() =>
    expect(JSON.parse(screen.getByLabelText('wire').textContent!)).toEqual([
      {
        sourceWorkItemId: 101,
        relationType: 'resolved_by_change',
        expectedVersion: 7,
        metadata: { required: true },
      },
    ])
  );
  expect(screen.getByText(/INC-REAL/)).toBeInTheDocument();
  (WorkItemRelationsApi.context as jest.Mock).mockRejectedValueOnce(new Error('权限已撤销'));
  fireEvent.click(screen.getByRole('button', { name: '刷新源版本（保留表单）' }));
  expect(await screen.findByText('权限已撤销')).toBeInTheDocument();
  expect(JSON.parse(screen.getByLabelText('wire').textContent!)[0].expectedVersion).toBe(7);
  (WorkItemRelationsApi.context as jest.Mock).mockResolvedValue({
    source: { workItemId: 101, number: 'INC-REAL', version: 8 },
    mutation: { allowed: true },
  });
  fireEvent.click(screen.getByRole('button', { name: '刷新源版本（保留表单）' }));
  await waitFor(() =>
    expect(JSON.parse(screen.getByLabelText('wire').textContent!)[0]).toEqual({
      sourceWorkItemId: 101,
      relationType: 'resolved_by_change',
      expectedVersion: 8,
      metadata: { required: true },
    })
  );
});
it('denied source cannot become a creation binding', async () => {
  (WorkItemRelationsApi.context as jest.Mock).mockResolvedValue({
    source: { workItemId: 101, version: 7 },
    mutation: { allowed: false, reason: '没有关联权限' },
  });
  render(<FormFixture />);
  fireEvent.change(screen.getByLabelText('源 WorkItem ID'), { target: { value: '101' } });
  fireEvent.click(screen.getByRole('button', { name: '读取并添加源记录' }));
  expect(await screen.findByText('没有关联权限')).toBeInTheDocument();
  expect(screen.getByLabelText('wire')).toHaveTextContent('[]');
  expect(screen.getByLabelText('源 WorkItem ID')).toHaveValue(101);
});

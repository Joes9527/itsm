import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { WorkItemAssignment, type AssignmentProps } from '../WorkItemAssignment';
const base: AssignmentProps = {
  currentAssigneeId: 1,
  version: 7,
  allowed: true,
  candidates: [
    { id: 1, label: 'Current' },
    { id: 8, label: 'Next' },
  ],
  submit: jest.fn(),
  refresh: jest.fn(),
};
function fill() {
  fireEvent.click(screen.getByTestId('workitem-assignment-open'));
  fireEvent.change(screen.getByLabelText('负责人'), { target: { value: '8' } });
  fireEvent.change(screen.getByLabelText('转派原因'), { target: { value: 'team ownership' } });
}
beforeEach(() => jest.clearAllMocks());
test('requires nonblank reassignment reason and submits once with frozen version', async () => {
  const submit = jest.fn().mockResolvedValue(undefined);
  const view = render(<WorkItemAssignment {...base} submit={submit} />);
  fireEvent.click(screen.getByTestId('workitem-assignment-open'));
  fireEvent.change(screen.getByLabelText('负责人'), { target: { value: '8' } });
  expect(screen.getByTestId('workitem-assignment-submit')).toBeDisabled();
  fireEvent.change(screen.getByLabelText('转派原因'), { target: { value: '  ' } });
  expect(screen.getByTestId('workitem-assignment-submit')).toBeDisabled();
  fireEvent.change(screen.getByLabelText('转派原因'), { target: { value: 'team ownership' } });
  view.rerender(<WorkItemAssignment {...base} version={8} submit={submit} />);
  fireEvent.click(screen.getByTestId('workitem-assignment-submit'));
  await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
  expect(submit).toHaveBeenCalledWith(
    expect.objectContaining({
      assigneeId: 8,
      reason: 'team ownership',
      version: 7,
      operationId: expect.any(String),
    })
  );
});
test('backend denial is visible', () => {
  render(<WorkItemAssignment {...base} allowed={false} disabledReason='权限已撤销' />);
  expect(screen.getByText('权限已撤销')).toBeVisible();
  expect(screen.getByTestId('workitem-assignment-open')).toBeDisabled();
});
test('409 preserves input and requires explicit refreshed-version confirmation before a new request', async () => {
  const submit = jest.fn().mockRejectedValueOnce({ status: 409 }).mockResolvedValue(undefined);
  const refresh = jest.fn().mockResolvedValue({ version: 9, currentAssigneeId: 2, allowed: true });
  render(<WorkItemAssignment {...base} submit={submit} refresh={refresh} />);
  fill();
  fireEvent.click(screen.getByTestId('workitem-assignment-submit'));
  await screen.findByTestId('workitem-assignment-conflict');
  expect(submit).toHaveBeenCalledTimes(1);
  expect(screen.getByLabelText('转派原因')).toHaveValue('team ownership');
  expect(screen.getByLabelText('负责人')).toHaveValue('8');
  expect(screen.getByTestId('workitem-assignment-submit')).toBeDisabled();
  fireEvent.click(screen.getByTestId('workitem-assignment-refresh'));
  await screen.findByTestId('workitem-assignment-confirm');
  expect(submit).toHaveBeenCalledTimes(1);
  expect(screen.getByTestId('workitem-assignment-submit')).toBeDisabled();
  fireEvent.click(screen.getByTestId('workitem-assignment-confirm'));
  fireEvent.click(screen.getByTestId('workitem-assignment-submit'));
  await waitFor(() => expect(submit).toHaveBeenCalledTimes(2));
  expect(submit.mock.calls[1][0].version).toBe(9);
  expect(submit.mock.calls[1][0].operationId).not.toBe(submit.mock.calls[0][0].operationId);
});
test('uncertain network retry reuses the operation identity', async () => {
  const submit = jest
    .fn()
    .mockRejectedValueOnce(new Error('network interrupted'))
    .mockResolvedValue(undefined);
  render(<WorkItemAssignment {...base} submit={submit} />);
  fill();
  fireEvent.click(screen.getByTestId('workitem-assignment-submit'));
  await screen.findByText('network interrupted');
  fireEvent.click(screen.getByTestId('workitem-assignment-submit'));
  await waitFor(() => expect(submit).toHaveBeenCalledTimes(2));
  expect(submit.mock.calls[1][0]).toEqual(submit.mock.calls[0][0]);
});

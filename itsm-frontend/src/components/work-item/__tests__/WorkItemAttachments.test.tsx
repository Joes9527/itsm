import React from 'react';
import { render, screen } from '@testing-library/react';
import { WorkItemAttachments } from '../WorkItemAttachments';
import { WorkItemProvider } from '../WorkItemContext';
import type { WorkItemCommon } from '../WorkItemTypes';

jest.mock('@/components/business/detail-tabs', () => ({
  AttachmentPanel: ({ targetType, targetId }: { targetType: string; targetId: number }) => (
    <div data-testid="attachment-panel" data-target-type={targetType} data-target-id={targetId} />
  ),
  ticketAttachmentAdapter: {},
}));

const workItem: WorkItemCommon = {
  id: 9,
  number: 'C-1',
  recordClass: 'change_request',
  title: 't',
  status: 's',
  priority: 'p',
  requesterId: 1,
  createdAt: '',
  updatedAt: '',
};

describe('WorkItemAttachments', () => {
  it('renders AttachmentPanel with the mapped targetType and the given workItemId', () => {
    render(
      <WorkItemProvider value={{ workItem, actions: {}, onActionDispatch: jest.fn() }}>
        <WorkItemAttachments workItemId={9} />
      </WorkItemProvider>
    );
    const panel = screen.getByTestId('attachment-panel');
    expect(panel).toHaveAttribute('data-target-type', 'change');
    expect(panel).toHaveAttribute('data-target-id', '9');
  });
});

import React from 'react';
import { render, screen } from '@testing-library/react';
import { WorkItemComments } from '../WorkItemComments';
import { WorkItemProvider } from '../WorkItemContext';
import type { WorkItemCommon } from '../WorkItemTypes';

jest.mock('@/components/business/detail-tabs', () => ({
  CommentPanel: ({ targetType, targetId }: { targetType: string; targetId: number }) => (
    <div data-testid="comment-panel" data-target-type={targetType} data-target-id={targetId} />
  ),
  ticketCommentAdapter: {},
}));

jest.mock('@/lib/store/auth-store', () => ({
  useAuthStore: () => ({ user: { id: 42 } }),
}));

const workItem: WorkItemCommon = {
  id: 7,
  number: 'PRB-1',
  recordClass: 'problem',
  title: 't',
  status: 's',
  priority: 'p',
  requesterId: 1,
  createdAt: '',
  updatedAt: '',
};

describe('WorkItemComments', () => {
  it('renders CommentPanel with the mapped targetType and the given workItemId', () => {
    render(
      <WorkItemProvider value={{ workItem, actions: {}, onActionDispatch: jest.fn() }}>
        <WorkItemComments workItemId={7} />
      </WorkItemProvider>
    );
    const panel = screen.getByTestId('comment-panel');
    expect(panel).toHaveAttribute('data-target-type', 'problem');
    expect(panel).toHaveAttribute('data-target-id', '7');
  });
});

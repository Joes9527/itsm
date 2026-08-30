import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { WorkItemActionBar } from '../WorkItemActionBar';
import { WorkItemProvider } from '../WorkItemContext';
import type { WorkItemCommon } from '../WorkItemTypes';

const workItem: WorkItemCommon = {
  id: 1,
  number: 'X-1',
  recordClass: 'incident',
  title: 't',
  status: 's',
  priority: 'p',
  requesterId: 1,
  createdAt: '',
  updatedAt: '',
};

describe('WorkItemActionBar', () => {
  it('renders nothing when actions is empty', () => {
    const { container } = render(
      <WorkItemProvider value={{ workItem, actions: {}, onActionDispatch: jest.fn() }}>
        <WorkItemActionBar />
      </WorkItemProvider>
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('renders an enabled button for an allowed action and calls onActionDispatch with the action name', () => {
    const dispatch = jest.fn().mockResolvedValue(undefined);
    render(
      <WorkItemProvider
        value={{ workItem, actions: { resolve: { allowed: true } }, onActionDispatch: dispatch }}
      >
        <WorkItemActionBar />
      </WorkItemProvider>
    );
    const button = screen.getByRole('button', { name: '解决' });
    expect(button).toBeEnabled();
    fireEvent.click(button);
    expect(dispatch).toHaveBeenCalledWith('resolve');
  });

  it('disables the button and surfaces the reason as its title when not allowed', () => {
    render(
      <WorkItemProvider
        value={{
          workItem,
          actions: { close: { allowed: false, reason: '必须先填写解决方案' } },
          onActionDispatch: jest.fn(),
        }}
      >
        <WorkItemActionBar />
      </WorkItemProvider>
    );
    const button = screen.getByRole('button', { name: '关闭' });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('title', '必须先填写解决方案');
  });

  it('falls back to the raw action key when there is no Chinese label for it', () => {
    render(
      <WorkItemProvider
        value={{
          workItem,
          actions: { convertToProblem: { allowed: true } },
          onActionDispatch: jest.fn(),
        }}
      >
        <WorkItemActionBar />
      </WorkItemProvider>
    );
    expect(screen.getByRole('button', { name: 'convertToProblem' })).toBeInTheDocument();
  });
});

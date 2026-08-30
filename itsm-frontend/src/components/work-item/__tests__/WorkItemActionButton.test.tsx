import React from 'react';
import { render, screen } from '@testing-library/react';
import { WorkItemActionButton } from '../WorkItemActionButton';

describe('WorkItemActionButton', () => {
  it('renders nothing when the action is absent', () => {
    const { container } = render(
      <WorkItemActionButton action={undefined} actionName='edit' button={{}}>
        编辑
      </WorkItemActionButton>
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('disables a denied action and exposes its reason', () => {
    render(
      <WorkItemActionButton
        action={{ allowed: false, reason: '无权限编辑' }}
        actionName='edit'
        button={{}}
      >
        编辑
      </WorkItemActionButton>
    );
    const button = screen.getByRole('button', { name: /编\s*辑/ });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('title', '无权限编辑');
    expect(screen.getByRole('note')).toHaveTextContent('无权限编辑');
  });

  it('preserves a caller disabled state for an allowed action', () => {
    render(
      <WorkItemActionButton
        action={{ allowed: true }}
        actionName='resolve'
        button={{ disabled: true }}
      >
        解决
      </WorkItemActionButton>
    );
    expect(screen.getByRole('button', { name: /解\s*决/ })).toBeDisabled();
  });
});

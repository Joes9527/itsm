import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MenuItems } from '../MenuItems';

it('opens a navigation group without leaving the page, then navigates via a child', async () => {
  const destinations: string[] = [];
  render(<MenuItems selectedKeys={[]} onMenuClick={path => destinations.push(path)} items={[{
    key: '/workflow', path: '/workflow', icon: null, label: '工作流', children: [
      { key: '/admin/workflows', path: '/admin/workflows', icon: null, label: '工作流管理' },
      { key: '/workflow/designer', path: '/workflow/designer', icon: null, label: '流程设计器' },
      { key: '/workflow/instances', path: '/workflow/instances', icon: null, label: '流程实例' },
    ],
  }]} />);
  fireEvent.click(screen.getByText('工作流', { exact: true }));
  await waitFor(() => expect(screen.getByText('工作流管理', { exact: true })).toBeVisible());
  expect(screen.getByText('流程设计器')).toBeVisible();
  expect(screen.getByText('流程实例')).toBeVisible();
  expect(destinations).toEqual([]);
  fireEvent.click(screen.getByText('工作流管理', { exact: true }));
  expect(destinations).toEqual(['/admin/workflows']);
});

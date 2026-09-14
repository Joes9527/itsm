import React from 'react';
import { App } from 'antd';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { AttachmentPanel } from '../AttachmentPanel';
import type { AttachmentAdapter } from '../types';
import { getAttachmentPermissions } from '@/components/work-item/toTargetType';

const attachment = {
  id: 17,
  fileName: 'diagnostic.txt',
  fileSize: 3,
  mimeType: 'text/plain',
  createdAt: '2026-09-14T00:00:00Z',
};
const permissions = { canRead: true, canUpload: true, canDelete: true };
let adapter: AttachmentAdapter;
beforeEach(() => {
  adapter = {
    list: jest.fn().mockResolvedValue([]),
    upload: jest.fn().mockResolvedValue(attachment),
    remove: jest.fn().mockResolvedValue(undefined),
    getDownloadUrl: (id, att) => `/api/v1/tickets/${id}/attachments/${att}`,
    getPreviewUrl: (id, att) => `/api/v1/tickets/${id}/attachments/${att}/preview`,
  };
});
const mount = (access = permissions) =>
  render(
    <App>
      <AttachmentPanel targetType='ticket' targetId={101} adapter={adapter} permissions={access} />
    </App>
  );

it('does not load or expose attachment actions without read permission', async () => {
  mount({ canRead: false, canUpload: false, canDelete: false });
  expect(await screen.findByRole('alert')).toHaveTextContent('无权');
  expect(adapter.list).not.toHaveBeenCalled();
  expect(screen.queryByRole('button', { name: '上传附件' })).not.toBeInTheDocument();
});

it('uploads from an empty list and shows the persisted result', async () => {
  const user = userEvent.setup();
  const { container } = mount();
  await screen.findByRole('button', { name: '上传附件' });
  (adapter.list as jest.Mock).mockResolvedValue([attachment]);
  const file = new File(['log'], 'diagnostic.txt', { type: 'text/plain' });
  await user.upload(container.querySelector('input[type=file]') as HTMLInputElement, file);
  expect(await screen.findByText('diagnostic.txt', { exact: true })).toBeVisible();
  expect(adapter.upload).toHaveBeenCalledWith(101, file, expect.any(Function));
});

it('requires confirmation before deletion and refreshes the list', async () => {
  (adapter.list as jest.Mock).mockResolvedValue([attachment]);
  const user = userEvent.setup();
  mount();
  await user.click(await screen.findByRole('button', { name: '删除' }));
  await user.click(
    within(await screen.findByRole('dialog')).getByRole('button', { name: /取\s*消/ })
  );
  expect(adapter.remove).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  await user.click(screen.getByRole('button', { name: /删\s*除/ }));
  (adapter.list as jest.Mock).mockResolvedValue([]);
  await user.click(
    within(await screen.findByRole('dialog')).getByRole('button', { name: /删\s*除/ })
  );
  await waitFor(() =>
    expect(screen.queryByText('diagnostic.txt', { exact: true })).not.toBeInTheDocument()
  );
  expect(adapter.remove).toHaveBeenCalledWith(101, 17);
});

it('does not grant deletion just because currentUserId is missing', async () => {
  (adapter.list as jest.Mock).mockResolvedValue([attachment]);
  mount({ canRead: true, canUpload: false, canDelete: false });
  await screen.findByText('diagnostic.txt');
  expect(screen.queryByRole('button', { name: '删除' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '上传附件' })).not.toBeInTheDocument();
});

it('previews through the protected endpoint', async () => {
  (adapter.list as jest.Mock).mockResolvedValue([attachment]);
  mount();
  const user = userEvent.setup();
  await user.click(await screen.findByRole('button', { name: '预览' }));
  expect(screen.getByTitle('diagnostic.txt')).toHaveAttribute(
    'src',
    '/api/v1/tickets/101/attachments/17/preview'
  );
});

it('keeps upload available after failure and can retry with the file', async () => {
  (adapter.upload as jest.Mock)
    .mockRejectedValueOnce(new Error('存储不可用'))
    .mockResolvedValueOnce(attachment);
  const user = userEvent.setup();
  const { container } = mount();
  await screen.findByRole('button', { name: '上传附件' });

  await user.upload(
    container.querySelector('input[type=file]') as HTMLInputElement,
    new File(['log'], 'diagnostic.txt')
  );
  await screen.findByText('存储不可用');
  (adapter.list as jest.Mock).mockResolvedValue([attachment]);
  await user.upload(
    container.querySelector('input[type=file]') as HTMLInputElement,
    new File(['log'], 'diagnostic.txt')
  );
  expect(await screen.findByText('diagnostic.txt', { exact: true })).toBeVisible();
  expect(adapter.upload).toHaveBeenCalledTimes(2);
});

it('rejects an oversized file before contacting storage', async () => {
  const user = userEvent.setup();
  const { container } = render(
    <App>
      <AttachmentPanel
        targetType='ticket'
        targetId={101}
        adapter={adapter}
        permissions={permissions}
        maxSize={2}
      />
    </App>
  );
  await screen.findByRole('button', { name: '上传附件' });
  await user.upload(
    container.querySelector('input[type=file]') as HTMLInputElement,
    new File(['large'], 'large.txt')
  );
  expect(await screen.findByText(/文件大小超过/)).toBeVisible();
  expect(adapter.upload).not.toHaveBeenCalled();
});

it('uses professional permissions and fails closed for an unknown class', () => {
  const check = (permission: string) => ['change:read', 'change:create'].includes(permission);
  expect(getAttachmentPermissions('change_request', check)).toEqual({
    canRead: true,
    canUpload: true,
    canDelete: false,
  });
  expect(getAttachmentPermissions('unknown', () => true)).toEqual({
    canRead: false,
    canUpload: false,
    canDelete: false,
  });
});

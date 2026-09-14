import React from 'react';
import { App } from 'antd';
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { AttachmentPanel } from '../AttachmentPanel';
import type { AttachmentAdapter } from '../types';
import { getAttachmentPermissions } from '@/components/work-item/toTargetType';
import { ApiError } from '@/lib/api/http-client';

const attachment = {
  id: 17,
  fileName: 'diagnostic.txt',
  fileSize: 3,
  mimeType: 'text/plain; charset=utf-8',
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
    preview: jest.fn().mockResolvedValue({ type: 'text/plain', text: async () => 'visible preview proof' }),
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
  expect(adapter.upload).toHaveBeenCalledWith(101, file, expect.any(Function), expect.any(Function));
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
  expect(adapter.remove).toHaveBeenCalledWith(101, 17, expect.any(Function));
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
  await waitFor(() => expect(screen.getByText('visible preview proof')).toBeVisible());
  expect(adapter.preview).toHaveBeenCalledWith(101, 17, expect.any(Function));
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
  const check = (permission: string) => ['change:read', 'change:write'].includes(permission);
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

it('clears attachments and disables mutations after read authorization is revoked', async () => {
  (adapter.list as jest.Mock).mockResolvedValueOnce([attachment]).mockRejectedValueOnce(new ApiError('权限已撤销', 403));
  mount(); await screen.findByText('diagnostic.txt');
  await userEvent.click(screen.getByRole('button', { name: '刷新' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('权限已撤销');
  expect(screen.queryByText('diagnostic.txt')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '上传附件' })).not.toBeInTheDocument();
});

it.each(['预览', '删除'])('closes the %s dialog when read permission is withdrawn', async action => {
  (adapter.list as jest.Mock).mockResolvedValue([attachment]);
  const onCountChange = jest.fn();
  const view = render(<App><AttachmentPanel targetType="ticket" targetId={101} adapter={adapter} permissions={permissions} onCountChange={onCountChange} /></App>);
  await userEvent.click(await screen.findByRole('button', { name: action }));
  await waitFor(() => expect(screen.getByRole('dialog')).toBeVisible());
  view.rerender(<App><AttachmentPanel targetType="ticket" targetId={101} adapter={adapter} permissions={{ canRead: false, canUpload: false, canDelete: false }} onCountChange={onCountChange} /></App>);
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  expect(screen.queryByText('diagnostic.txt')).not.toBeInTheDocument();
  expect(onCountChange).toHaveBeenLastCalledWith(undefined);
  expect(adapter.remove).not.toHaveBeenCalled();
});
it('refuses active content even when the filename advertised a text preview', async () => {
  (adapter.list as jest.Mock).mockResolvedValue([attachment]);
  (adapter.preview as jest.Mock).mockResolvedValue({ type: 'text/html', text: async () => '<script>danger</script>' });
  mount();
  await userEvent.click(await screen.findByRole('button', { name: '预览' }));
  expect(await screen.findByText('该文件类型不支持安全预览，请下载查看')).toBeInTheDocument();
  expect(document.querySelector('iframe')).toBeNull();
});

it('clears the list when the protected preview endpoint denies access', async () => {
  (adapter.list as jest.Mock).mockResolvedValue([attachment]);
  (adapter.preview as jest.Mock).mockRejectedValue(new ApiError('预览权限撤销', 403));
  mount();
  await userEvent.click(await screen.findByRole('button', { name: '预览' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('预览权限撤销');
  expect(screen.queryByText('diagnostic.txt')).not.toBeInTheDocument();
});
it('releases an image preview URL when the dialog closes', async () => {
  const createURL = jest.fn().mockReturnValue('blob:preview');
  const revokeURL = jest.fn();
  Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createURL });
  Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeURL });
  (adapter.list as jest.Mock).mockResolvedValue([{ ...attachment, mimeType: 'image/png' }]);
  (adapter.preview as jest.Mock).mockResolvedValue({ type: 'image/png' });
  mount();
  await userEvent.click(await screen.findByRole('button', { name: '预览' }));
  expect(await screen.findByAltText('diagnostic.txt')).toHaveAttribute('src', 'blob:preview');
  await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: /close/i }));
  await waitFor(() => expect(revokeURL).toHaveBeenCalledWith('blob:preview'));
});

it('does not allocate an image URL for a preview that was closed while loading', async () => {
  const createURL = jest.fn();
  Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createURL });
  let finish!: (value: unknown) => void;
  (adapter.list as jest.Mock).mockResolvedValue([{ ...attachment, mimeType: 'image/png' }]);
  (adapter.preview as jest.Mock).mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
  mount();
  await userEvent.click(await screen.findByRole('button', { name: '预览' }));
  await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: /close/i }));
  await act(async () => finish({ type: 'image/png' }));
  expect(createURL).not.toHaveBeenCalled();
});
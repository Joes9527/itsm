import { ticketCommentAdapter } from '@/components/business/detail-tabs/adapters/ticket-comment-adapter';
import { ticketAttachmentAdapter } from '@/components/business/detail-tabs/adapters/ticket-attachment-adapter';
import { security } from '@/lib/security';
jest.mock('@/lib/env', () => ({ logger: { debug: jest.fn(), warn: jest.fn(), error: jest.fn() } }));
jest.mock('@/lib/security', () => ({
  security: {
    csrf: { getToken: jest.fn(), clearToken: jest.fn() },
    network: { getSecureHeaders: () => ({}) },
  },
}));
const writes: Array<[string, (assertCurrent: () => void) => Promise<unknown>]> = [
  ['comment create', guard => ticketCommentAdapter.create(101, { content: 'text' }, guard)],
  ['comment update', guard => ticketCommentAdapter.update!(101, 7, { content: 'text' }, guard)],
  ['comment delete', guard => ticketCommentAdapter.remove(101, 7, guard)],
  ['attachment delete', guard => ticketAttachmentAdapter.remove(101, 7, guard)],
];
it.each(writes)('fences %s if context expires while CSRF is pending', async (_label, write) => {
  let current = true;
  global.fetch = jest.fn();
  jest.mocked(security.csrf.getToken).mockImplementationOnce(async () => {
    current = false;
    return 'csrf';
  });
  await expect(
    write(() => {
      if (!current) throw new Error('context changed');
    })
  ).rejects.toThrow('context changed');
  expect(global.fetch).not.toHaveBeenCalled();
});

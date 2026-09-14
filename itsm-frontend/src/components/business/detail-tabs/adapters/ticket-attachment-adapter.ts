import { TicketAttachmentApi } from '@/lib/api/ticket-attachment-api';
import type { AttachmentAdapter, AttachmentItem } from '../types';

export const ticketAttachmentAdapter: AttachmentAdapter = {
  async list(targetId) {
    const res = await TicketAttachmentApi.listAttachments(Number(targetId));
    return (res.attachments || []) as unknown as AttachmentItem[];
  },
  async upload(targetId, file, onProgress, assertSubmissionContext) {
    const res = await TicketAttachmentApi.uploadAttachment(
      Number(targetId),
      file,
      onProgress,
      assertSubmissionContext
    );
    return res as unknown as AttachmentItem;
  },
  getDownloadUrl(targetId, attachmentId) {
    return TicketAttachmentApi.getDownloadUrl(Number(targetId), attachmentId);
  },
  preview(targetId, attachmentId, assertSubmissionContext) {
    return TicketAttachmentApi.previewAttachment(
      Number(targetId),
      attachmentId,
      assertSubmissionContext
    );
  },
  async remove(targetId, attachmentId, assertSubmissionContext) {
    await TicketAttachmentApi.deleteAttachment(
      Number(targetId),
      attachmentId,
      assertSubmissionContext
    );
  },
};

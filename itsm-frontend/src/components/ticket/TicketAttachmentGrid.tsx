'use client';

import React, { useCallback, useEffect, useState } from 'react';
import { FileText, Download, Paperclip } from 'lucide-react';
import { ticketAttachmentAdapter } from '@/components/business/detail-tabs';
import type { AttachmentItem } from '@/components/business/detail-tabs';

interface TicketAttachmentGridProps {
  ticketId: number;
}

function formatSize(bytes?: number): string {
  if (!bytes || bytes <= 0) return '-';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/**
 * 工单工作台附件网格：视觉对齐 prototype 的缩略图卡片，
 * 数据仍走 ticketAttachmentAdapter。
 */
export const TicketAttachmentGrid: React.FC<TicketAttachmentGridProps> = ({ ticketId }) => {
  const [attachments, setAttachments] = useState<AttachmentItem[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchAttachments = useCallback(async () => {
    setLoading(true);
    try {
      const data = await ticketAttachmentAdapter.list(ticketId);
      setAttachments(Array.isArray(data) ? data : []);
    } catch {
      setAttachments([]);
    } finally {
      setLoading(false);
    }
  }, [ticketId]);

  useEffect(() => {
    void fetchAttachments();
  }, [fetchAttachments]);

  if (loading) {
    return (
      <div className="p-6 text-center text-[12px] text-muted">附件加载中...</div>
    );
  }

  if (attachments.length === 0) {
    return (
      <div className="text-center py-6 text-muted">
        <Paperclip className="w-8 h-8 mx-auto mb-2 text-muted" />
        <span className="text-[12px]">暂无附件</span>
      </div>
    );
  }

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 pt-2">
      {attachments.map(att => {
        const isImage = (att.mimeType || '').startsWith('image/');
        const previewUrl = att.fileUrl;
        const downloadUrl =
          typeof ticketAttachmentAdapter.getDownloadUrl === 'function'
            ? ticketAttachmentAdapter.getDownloadUrl(ticketId, att.id)
            : previewUrl;
        const uploader = att.uploader?.name || att.uploader?.username || '未知';

        return (
          <div
            key={att.id}
            className="flex items-center gap-3 p-3 bg-raised hover:bg-raised rounded-[8px] border border-border transition-all group"
          >
            {isImage && previewUrl ? (
              <div className="w-12 h-12 rounded-[8px] bg-border overflow-hidden shrink-0 border border-border">
                <img src={previewUrl} alt={att.fileName} className="w-full h-full object-cover" />
              </div>
            ) : (
              <div className="w-12 h-12 rounded-[8px] bg-raised text-muted flex items-center justify-center shrink-0 border border-border">
                <FileText size={22} />
              </div>
            )}
            <div className="min-w-0 flex-1 space-y-0.5">
              <p className="text-[12px] font-medium text-foreground truncate m-0 group-hover:text-orange-600 transition-colors">
                {att.fileName}
              </p>
              <span className="text-[11px] text-muted font-mono block">
                {formatSize(att.fileSize)} ｜ 上传者: {uploader}
              </span>
            </div>
            {downloadUrl && (
              <a
                href={downloadUrl}
                download={att.fileName}
                target="_blank"
                rel="noreferrer"
                className="w-7 h-7 rounded-[8px] bg-surface text-muted hover:text-orange-600 flex items-center justify-center border border-border shadow-none shrink-0"
                title="下载附件"
              >
                <Download size={12} />
              </a>
            )}
          </div>
        );
      })}
    </div>
  );
};

export default TicketAttachmentGrid;

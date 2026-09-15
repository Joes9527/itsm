'use client';

import React from 'react';
import { useDetailResource } from '@/components/business/detail-tabs/useDetailResource';
import { useDetailRefreshEntry } from '@/components/business/detail-tabs/DetailRefreshContext';
import { DetailReadState } from '@/components/business/detail-tabs/DetailReadState';

import { History as HistoryIcon } from 'lucide-react';
import { TicketApi } from '@/lib/api/ticket-api';

interface TicketHistoryListProps {
  ticketId: number;
  onCountChange?: (count: number | undefined) => void;
  formatDateTime?: (s: string) => string;
}

interface HistoryRow {
  id: number;
  action?: string;
  createdAt?: string;
  user?: { name?: string; username?: string };
  fieldName?: string;
  oldValue?: string;
  newValue?: string;
  changeReason?: string;
}

const defaultFormat = (s?: string) => (s ? new Date(s).toLocaleString('zh-CN') : '');

function mapHistory(raw: unknown): HistoryRow[] {
  const list = Array.isArray(raw) ? raw : [];
  return list.map(item => {
    const r = item as Record<string, unknown>;
    return {
      id: Number(r.id ?? 0),
      action: r.action as string | undefined,
      createdAt: String(r.createdAt ?? r.changedAt ?? ''),
      user: (r.user as { name?: string; username?: string }) ?? undefined,
      fieldName: r.fieldName as string | undefined,
      oldValue: r.oldValue as string | undefined,
      newValue: r.newValue as string | undefined,
      changeReason: r.changeReason as string | undefined,
    };
  });
}

/**
 * 工单工作台历史流转：视觉对齐 prototype 的条目行，
 * 数据与 HistoryTimeline 同一来源（TicketApi.getTicketHistory）。
 */
export const TicketHistoryList: React.FC<TicketHistoryListProps> = ({
  ticketId,
  onCountChange,
  formatDateTime = defaultFormat,
}) => {
  const resource = useDetailResource(ticketId, async () => mapHistory(await TicketApi.getTicketHistory(ticketId)), rows => rows.length, onCountChange);
  useDetailRefreshEntry({ key: 'history', label: '历史流转', reload: resource.reload, isWriting: () => false });
  const rows = resource.data || [];
  const feedback = <DetailReadState error={resource.error} loading={resource.loading} reload={resource.reload} />;
  if (!resource.ready) return <div>{feedback}{resource.loading && <p>历史加载中...</p>}</div>;

  if (rows.length === 0) {
    return (
      <div className="text-center py-6 text-muted">
        {feedback}
        <HistoryIcon className="w-8 h-8 mx-auto mb-2 text-muted" />
        <span className="text-[12px]">暂无流转历史</span>
      </div>
    );
  }

  return (
    <div className="space-y-2.5 pt-2 text-[12px]">
      {feedback}
      {rows.map(row => {
        const userName = row.user?.name || row.user?.username || '系统';
        const detail =
          row.oldValue || row.newValue
            ? `旧值: ${row.oldValue ?? '-'} → 新值: ${row.newValue ?? '-'}`
            : row.changeReason || row.fieldName;
        return (
          <div
            key={row.id}
            className="p-3 bg-raised rounded-[8px] border border-border flex items-center justify-between gap-3"
          >
            <div className="space-y-0.5 min-w-0">
              <span className="font-semibold text-foreground">
                {userName} {row.action || '更新了工单'}
              </span>
              {detail && (
                <p className="text-[11px] text-muted m-0 truncate">
                  {detail}
                </p>
              )}
            </div>
            <span className="text-[11px] text-muted font-mono shrink-0">
              {row.createdAt ? formatDateTime(row.createdAt) : ''}
            </span>
          </div>
        );
      })}
    </div>
  );
};

export default TicketHistoryList;

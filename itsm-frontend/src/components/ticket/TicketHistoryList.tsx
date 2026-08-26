'use client';

import React, { useCallback, useEffect, useState } from 'react';
import { History as HistoryIcon } from 'lucide-react';
import { TicketApi } from '@/lib/api/ticket-api';

interface TicketHistoryListProps {
  ticketId: number;
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
  formatDateTime = defaultFormat,
}) => {
  const [rows, setRows] = useState<HistoryRow[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchHistory = useCallback(async () => {
    setLoading(true);
    try {
      const raw = await TicketApi.getTicketHistory(ticketId);
      setRows(mapHistory(raw));
    } catch {
      setRows([]);
    } finally {
      setLoading(false);
    }
  }, [ticketId]);

  useEffect(() => {
    void fetchHistory();
  }, [fetchHistory]);

  if (loading) {
    return <div className="p-6 text-center text-xs text-slate-400">历史加载中...</div>;
  }

  if (rows.length === 0) {
    return (
      <div className="text-center py-6 text-slate-400">
        <HistoryIcon className="w-8 h-8 mx-auto mb-2 text-slate-300" />
        <span className="text-xs">暂无流转历史</span>
      </div>
    );
  }

  return (
    <div className="space-y-2.5 pt-2 text-xs">
      {rows.map(row => {
        const userName = row.user?.name || row.user?.username || '系统';
        const detail = row.oldValue || row.newValue
          ? `旧值: ${row.oldValue ?? '-'} → 新值: ${row.newValue ?? '-'}`
          : row.changeReason || row.fieldName;
        return (
          <div
            key={row.id}
            className="p-3 bg-slate-50 rounded-xl border border-slate-100 flex items-center justify-between gap-3"
          >
            <div className="space-y-0.5 min-w-0">
              <span className="font-semibold text-slate-800">
                {userName} {row.action || '更新了工单'}
              </span>
              {detail && <p className="text-[11px] text-slate-400 m-0 truncate">{detail}</p>}
            </div>
            <span className="text-[11px] text-slate-400 font-mono shrink-0">
              {row.createdAt ? formatDateTime(row.createdAt) : ''}
            </span>
          </div>
        );
      })}
    </div>
  );
};

export default TicketHistoryList;

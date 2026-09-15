'use client';
import { useDetailRefreshEntry } from '@/components/business/detail-tabs/DetailRefreshContext';

import React from 'react';
import { useDetailResource } from '@/components/business/detail-tabs/useDetailResource';
import { DetailReadState } from '@/components/business/detail-tabs/DetailReadState';
import { Tag } from 'antd';
import { Link2 } from 'lucide-react';
import { TicketRelationsApi } from '@/lib/api/ticket-relations-api';
import type { TicketRelationWithDetails } from '@/types/ticket-relations';
import { TicketStatus, TicketStatusConfig } from '@/constants/taxonomy';

interface TicketRelationCardsProps {
  ticketId: number;
  onCountChange?: (count: number | undefined) => void;
}

const relationTypeLabels: Record<string, string> = {
  parent_child: '父子关系',
  BLOCKS: '阻塞',
  BLOCKED_BY: '被阻塞',
  DEPENDS_ON: '依赖于',
  RELATES_TO: '相关',
  DUPLICATES: '重复',
  DUPLICATED_BY: '被重复',
  CAUSES: '导致',
  CAUSED_BY: '由...导致',
  REPLACES: '替代',
  REPLACED_BY: '被替代',
  SPLITS_FROM: '分离自',
  MERGED_INTO: '合并到',
};

const statusColor = (status?: string): string => {
  switch (status) {
    case 'resolved':
    case 'approved':
      return 'success';
    case 'closed':
      return 'default';
    case 'in_progress':
    case 'assigned':
      return 'processing';
    case 'new':
    case 'open':
    case 'pending':
      return 'warning';
    case 'rejected':
    case 'cancelled':
      return 'error';
    default:
      return 'default';
  }
};

const statusLabel = (status?: string): string => {
  if (!status) return '';
  return TicketStatusConfig[status as TicketStatus]?.label ?? status;
};

/**
 * 工单工作台关联卡片：视觉与字段完全对齐 prototype 的极简卡片样式，
 * 数据走 TicketRelationsApi.getTicketRelations（与 RelationPanel 同一数据源）。
 */
export const TicketRelationCards: React.FC<TicketRelationCardsProps> = ({
  ticketId,
  onCountChange,
}) => {
  const resource = useDetailResource<TicketRelationWithDetails[]>(
    ticketId,
    () => TicketRelationsApi.getTicketRelations(ticketId, { includeDetails: true }),
    data => data.length,
    onCountChange
  );
  const relations = resource.data || [];
  useDetailRefreshEntry(!resource.denied ? { key: 'relations', label: '关联关系', reload: resource.reload, isWriting: () => false } : undefined);
  const feedback = (
    <DetailReadState error={resource.error} loading={resource.loading} reload={resource.reload} />
  );
  if (!resource.ready)
    return (
      <div>
        {feedback}
        {resource.loading && <p>关联加载中...</p>}
      </div>
    );
  if (relations.length === 0) {
    return (
      <div className='text-center py-6 text-muted'>
        {feedback}
        <Link2 className='w-8 h-8 mx-auto mb-2 text-muted' />
        <span className='text-xs'>暂无关联工单</span>
      </div>
    );
  }

  return (
    <div className="space-y-2.5 pt-2 text-[12px]">
      {feedback}
      {relations.map(relation => {
        const isOutbound = relation.sourceTicketId === ticketId;
        const otherTicket = isOutbound ? relation.targetTicket : relation.sourceTicket;
        const otherNumber = isOutbound ? relation.targetTicketNumber : relation.sourceTicketNumber;
        const otherId = isOutbound ? relation.targetTicketId : relation.sourceTicketId;
        const otherTitle = otherTicket?.title || '无标题';
        const otherStatus = otherTicket?.status;
        const description =
          relation.description ||
          `${relationTypeLabels[relation.relationType] ?? relation.relationType} · ${
            isOutbound ? '本单指向对方' : '对方指向本单'
          }`;

        return (
          <div
            key={relation.id}
            className="p-4 bg-raised rounded-[8px] border border-border text-[12px] space-y-2"
          >
            <div className="flex items-center justify-between gap-2 font-medium text-foreground">
              <span className="truncate">
                关联工单: {otherNumber || `#${otherId}`} ({otherTitle})
              </span>
              {otherStatus && (
                <Tag color={statusColor(otherStatus)}>{statusLabel(otherStatus)}</Tag>
              )}
            </div>
            <p className="text-[11px] text-muted m-0">{description}</p>
          </div>
        );
      })}
    </div>
  );
};

export default TicketRelationCards;

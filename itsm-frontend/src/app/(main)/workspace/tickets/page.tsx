'use client';

import TicketDetail from '@/components/ticket/TicketDetail';
import { AssignedTicketQueue } from './_components/AssignedTicketQueue';
import { useAssignedTickets } from './_hooks/useAssignedTickets';

export default function WorkspaceTicketsPage() {
  const queue = useAssignedTickets();
  return (
    <div className='grid min-w-0 grid-cols-1 items-start gap-4 xl:grid-cols-[280px_minmax(0,1fr)]'>
      <AssignedTicketQueue queue={queue} />
      <section aria-label='工单处理详情' className='min-w-0'>
        {queue.selectedId !== undefined ? (
          <TicketDetail
            key={`${queue.identity}:${queue.selectedId}`}
            id={String(queue.selectedId)}
          />
        ) : (
          <div className='rounded-[8px] border border-border bg-surface p-6 text-sm text-muted'>
            请从个人队列选择工单进行处理
          </div>
        )}
      </section>
    </div>
  );
}

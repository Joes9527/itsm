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
          <div className='rounded-2xl border border-slate-200 bg-white p-6 text-sm text-slate-500 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-400'>
            请从个人队列选择工单进行处理
          </div>
        )}
      </section>
    </div>
  );
}

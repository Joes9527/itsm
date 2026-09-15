'use client';

import { Alert, Button, Empty, Pagination, Spin } from 'antd';
import type { useAssignedTickets } from '../_hooks/useAssignedTickets';

type QueueState = ReturnType<typeof useAssignedTickets>;
const unavailableQueues = [
  ['未分配池', '待接入分组队列'],
  ['即将超时', '待接入 SLA 队列'],
  ['待用户回复', '待明确状态契约'],
];

export function AssignedTicketQueue({ queue }: { queue: QueueState }) {
  return (
    <aside
      aria-label='个人工单队列'
      className='min-w-0 rounded-lg border border-border bg-surface p-4'
    >
      <div className='mb-3 flex flex-wrap items-center justify-between gap-2'>
        <h2 className='m-0 text-[15px] font-semibold text-foreground'>
          分给我的{queue.total !== undefined ? ` (${queue.total})` : ''}
        </h2>
        <Button
          size='small'
          loading={queue.loading}
          onClick={() => void queue.reload()}
          aria-label='刷新队列'
        >
          刷新
        </Button>
      </div>
      <div className='mb-4 grid gap-2'>
        {unavailableQueues.map(([name, reason]) => (
          <button
            key={name}
            type='button'
            disabled
            className='rounded-lg border border-border p-2 text-left text-[12px] text-muted'
          >
            <span className='block font-medium'>{name}</span>
            <span>{reason}</span>
          </button>
        ))}
      </div>
      {queue.error && (
        <Alert
          className='mb-3'
          type={queue.denied ? 'warning' : 'error'}
          showIcon
          title={queue.error}
          action={
            <Button size='small' onClick={() => void queue.reload()} aria-label='重试队列'>
              重试
            </Button>
          }
        />
      )}
      {queue.loading && !queue.items.length && (
        <div role='status' aria-label='加载队列' className='p-6 text-center'>
          <Spin />
        </div>
      )}
      {!queue.loading && !queue.error && !queue.items.length && (
        <Empty description='暂无分配给您的工单' image={Empty.PRESENTED_IMAGE_SIMPLE} />
      )}
      <ul className='m-0 grid list-none gap-2 p-0' aria-label='分配工单'>
        {queue.items.map(item => (
          <li key={item.id}>
            <button
              type='button'
              aria-pressed={item.id === queue.selectedId}
              onClick={() => queue.select(item.id)}
              className={`w-full min-w-0 rounded-lg border p-3 text-left transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-600 ${
                item.id === queue.selectedId
                  ? 'border-primary-600 bg-selected'
                  : 'border-border hover:bg-raised'
              }`}
            >
              <span className='block break-all font-mono text-[12px] text-muted'>
                {item.ticketNumber || `#${item.id}`}
              </span>
              <span className='mt-1 block break-words text-[13px] font-medium text-foreground'>
                {item.title}
              </span>
            </button>
          </li>
        ))}
      </ul>
      {queue.total !== undefined && queue.total > 0 && (
        <div className='mt-4'>
          <Pagination
            simple
            current={queue.page}
            total={queue.total}
            pageSize={20}
            showSizeChanger={false}
            onChange={queue.changePage}
            disabled={queue.loading}
          />
        </div>
      )}
    </aside>
  );
}

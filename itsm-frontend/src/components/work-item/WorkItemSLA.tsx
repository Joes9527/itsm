'use client';

import React from 'react';
import { Card, Progress, Tag } from 'antd';
import { Clock } from 'lucide-react';
import type { WorkItemSLAState } from './WorkItemTypes';

const timestamp = (value: string | null | undefined) =>
  value ? new Date(value).toLocaleString() : '未记录';

export function WorkItemSLA({ sla }: { sla?: WorkItemSLAState }) {
  if (!sla) return null;
  const unavailable = sla.slaStatus === 'not_required' || sla.slaStatus === 'configuration_missing';
  return (
    <Card
      size='small'
      title={
        <span className='flex items-center gap-1.5'>
          <Clock size={14} />
          SLA 时效与承诺
        </span>
      }
      extra={
        sla.slaName ? <Tag color={sla.isBreached ? 'red' : 'blue'}>{sla.slaName}</Tag> : undefined
      }
    >
      {sla.slaStatus === 'not_required' && <Tag>无需 SLA</Tag>}
      {sla.slaStatus === 'configuration_missing' && <Tag color='red'>SLA 配置缺失</Tag>}
      {!unavailable && (
        <div className='space-y-3 text-xs'>
          <div className='font-medium'>当前周期 #{sla.cycleNumber ?? 1}</div>
          {sla.cycleStartedAt && <div>周期开始：{timestamp(sla.cycleStartedAt)}</div>}
          <div>暂停累计：{sla.pausedMinutes ?? 0} 分钟</div>
          {sla.closedAt && <div>周期已关闭：{timestamp(sla.closedAt)}</div>}
          {sla.isBreached && <Tag color='red'>SLA 已违规</Tag>}
          {[
            {
              name: '响应',
              deadline: sla.responseDeadline,
              completed: sla.firstResponseAt,
              total: sla.responseTime,
              remaining: sla.responseTimeRemaining,
            },
            {
              name: '解决',
              deadline: sla.resolutionDeadline,
              completed: sla.resolvedAt,
              total: sla.resolutionTime,
              remaining: sla.resolutionTimeRemaining,
            },
          ].map(clock => (
            <div
              key={clock.name}
              className='bg-slate-50 p-3 rounded-xl border border-slate-100 space-y-2'
            >
              {clock.deadline && (
                <div>
                  <span>{clock.name}截止:</span> {timestamp(clock.deadline)}
                  {clock.remaining !== null && clock.remaining < 0 && ' (已超时)'}
                </div>
              )}
              {clock.completed ? (
                <div>
                  已{clock.name}：{timestamp(clock.completed)}
                </div>
              ) : sla.closedAt ? (
                <div>{clock.name}计时已停止</div>
              ) : (
                <>
                  <div>
                    {clock.name}进度 ·{' '}
                    {clock.remaining === null ? '未记录' : `剩余 ${clock.remaining} 分钟`}
                  </div>
                  {clock.total > 0 && (
                    <Progress
                      size='small'
                      percent={
                        clock.remaining === null
                          ? 0
                          : Math.min(
                              100,
                              Math.max(
                                0,
                                Math.round(((clock.total - clock.remaining) / clock.total) * 100)
                              )
                            )
                      }
                      status={
                        clock.remaining !== null && clock.remaining < 0 ? 'exception' : 'normal'
                      }
                    />
                  )}
                </>
              )}
              {clock.total > 0 && <div>目标 {clock.total} 分钟</div>}
            </div>
          ))}
        </div>
      )}
      {(sla.history ?? []).map(cycle => (
        <div key={cycle.number} className='mt-3 border-t pt-3 space-y-1 text-xs'>
          <div className='font-medium'>历史周期 #{cycle.number}</div>
          <div>
            {timestamp(cycle.startedAt)} — {timestamp(cycle.endedAt)}
          </div>
          <div>策略：{cycle.policy?.name ?? '策略未记录'}</div>
          <div>
            响应完成：{timestamp(cycle.responseAt)} · 响应截止：{timestamp(cycle.responseDeadline)}
          </div>
          <Tag color={cycle.responseBreached ? 'red' : undefined}>
            {cycle.responseBreached
              ? '响应已违规'
              : cycle.responseDeadline
                ? '响应未违规'
                : '响应未计时'}
          </Tag>
          <div>
            解决完成：{timestamp(cycle.resolvedAt)} · 解决截止：
            {timestamp(cycle.resolutionDeadline)}
          </div>
          <Tag color={cycle.resolutionBreached ? 'red' : undefined}>
            {cycle.resolutionBreached
              ? '解决已违规'
              : cycle.resolutionDeadline
                ? '解决未违规'
                : '解决未计时'}
          </Tag>
          <div>暂停累计：{cycle.pausedMinutes} 分钟</div>
        </div>
      ))}
    </Card>
  );
}

export default WorkItemSLA;

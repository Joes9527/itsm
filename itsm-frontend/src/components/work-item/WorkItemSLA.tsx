'use client';

import React from 'react';
import { Card, Progress, Tag } from 'antd';
import { Clock } from 'lucide-react';
import type { WorkItemSLAState } from './WorkItemTypes';

// 视觉与字段完全对齐 TicketDetail.tsx 现有的 SLA 卡片（响应/解决倒计时 + 超时高亮），
// 不新建视觉规范——见设计文档 §5.2。
const getSLAPercent = (total: number, remaining: number | null): number => {
  if (!total || total <= 0 || remaining === null) return 0;
  return Math.min(100, Math.max(0, Math.round(((total - remaining) / total) * 100)));
};

const formatHours = (minutes: number): string => (minutes / 60).toFixed(1);

export function WorkItemSLA({ sla }: { sla?: WorkItemSLAState }) {
  if (!sla) {
    return null;
  }

  return (
    <Card
      size="small"
      title={
        <span className="flex items-center gap-1.5">
          <Clock size={14} className="text-slate-500" />
          SLA 时效与承诺
        </span>
      }
      extra={<Tag color={sla.isBreached ? 'red' : 'blue'}>{sla.slaName}</Tag>}
    >
      <div className="bg-slate-50 p-3 rounded-xl border border-slate-100 space-y-2 text-xs">
        {sla.responseDeadline && (
          <div className="flex items-center justify-between">
            <span className="text-slate-500 text-[11px]">响应截止:</span>
            <span
              className={`font-mono text-xs ${
                sla.responseTimeRemaining !== null && sla.responseTimeRemaining < 0
                  ? 'text-red-600 font-bold'
                  : 'text-slate-800'
              }`}
            >
              {new Date(sla.responseDeadline).toLocaleString()}
              {sla.responseTimeRemaining !== null && sla.responseTimeRemaining < 0 && ' (已超时)'}
            </span>
          </div>
        )}

        {sla.resolutionDeadline && (
          <div className="flex items-center justify-between">
            <span className="text-slate-500 text-[11px]">解决截止:</span>
            <span
              className={`font-mono text-xs ${
                sla.resolutionTimeRemaining !== null && sla.resolutionTimeRemaining < 0
                  ? 'text-red-600 font-bold'
                  : 'text-slate-800'
              }`}
            >
              {new Date(sla.resolutionDeadline).toLocaleString()}
              {sla.resolutionTimeRemaining !== null && sla.resolutionTimeRemaining < 0 && ' (已超时)'}
            </span>
          </div>
        )}

        {sla.isBreached && (
          <div className="pt-1">
            <Tag color="red" className="w-full text-center">
              SLA 已违规
            </Tag>
          </div>
        )}

        {sla.responseTime > 0 && (
          <div className="space-y-1">
            <div className="flex justify-between text-[11px] text-slate-500">
              <span>响应进度</span>
              <span>
                {sla.responseTimeRemaining !== null ? `剩余 ${sla.responseTimeRemaining} 分钟` : '--'}
              </span>
            </div>
            <Progress
              percent={getSLAPercent(sla.responseTime, sla.responseTimeRemaining)}
              size="small"
              strokeColor={
                sla.responseTimeRemaining !== null && sla.responseTimeRemaining < 0
                  ? '#ff4d4f'
                  : getSLAPercent(sla.responseTime, sla.responseTimeRemaining) >= 70
                    ? '#fa8c16'
                    : '#52c41a'
              }
            />
            <div className="flex justify-between text-[11px] text-slate-400 font-mono">
              <span>
                {sla.responseTimeRemaining !== null
                  ? `剩余 ${formatHours(sla.responseTimeRemaining)} 小时`
                  : '--'}
              </span>
              <span>目标 {formatHours(sla.responseTime)} 小时</span>
            </div>
          </div>
        )}

        {sla.resolutionTime > 0 && (
          <div className="space-y-1">
            <div className="flex justify-between text-[11px] text-slate-500">
              <span>解决进度</span>
              <span>
                {sla.resolutionTimeRemaining !== null
                  ? `剩余 ${sla.resolutionTimeRemaining} 分钟`
                  : '--'}
              </span>
            </div>
            <Progress
              percent={getSLAPercent(sla.resolutionTime, sla.resolutionTimeRemaining)}
              size="small"
              strokeColor={
                sla.resolutionTimeRemaining !== null && sla.resolutionTimeRemaining < 0
                  ? '#ff4d4f'
                  : getSLAPercent(sla.resolutionTime, sla.resolutionTimeRemaining) >= 70
                    ? '#fa8c16'
                    : '#52c41a'
              }
            />
            <div className="flex justify-between text-[11px] text-slate-400 font-mono">
              <span>
                {sla.resolutionTimeRemaining !== null
                  ? `剩余 ${formatHours(sla.resolutionTimeRemaining)} 小时`
                  : '--'}
              </span>
              <span>目标 {formatHours(sla.resolutionTime)} 小时</span>
            </div>
          </div>
        )}
      </div>
    </Card>
  );
}

export default WorkItemSLA;

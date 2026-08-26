'use client';

import React, { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { Button, Empty, message } from 'antd';
import { PlayCircle, ExternalLink } from 'lucide-react';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { serviceRequestAPI } from '@/lib/api/service-request-api';
import type { ProvisioningTask } from '@/lib/api/service-request-api';

interface ServiceRequestPanelProps {
  ticketId: number;
}

// 服务目录来源的工单，在工单详情页里额外展示的补充信息面板。
// 样式对齐 prototype：规格字段网格 + 头部常驻「开始交付」+ 交付任务状态动效。
export default function ServiceRequestPanel({ ticketId }: ServiceRequestPanelProps) {
  const router = useRouter();
  const [loading, setLoading] = useState(true);
  const [request, setRequest] = useState<any>(null);
  const [tasks, setTasks] = useState<ProvisioningTask[]>([]);
  const [starting, setStarting] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      const data = await ServiceCatalogApi.getServiceRequestByTicketId(ticketId);
      setRequest(data);
      if (data?.id) {
        const taskList = await serviceRequestAPI.listProvisioningTasks(data.id);
        setTasks(taskList || []);
      }
    } catch {
      // 这个 ticket 不是服务目录来源，或者查询失败——不渲染面板即可，不当错误处理
      setRequest(null);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, [ticketId]);

  const handleStartProvisioning = async () => {
    if (!request?.id) return;
    setStarting(true);
    try {
      await serviceRequestAPI.startProvisioning(request.id);
      message.success('已开始交付');
      load();
    } catch (e: any) {
      message.error(e?.message || '启动交付失败');
    } finally {
      setStarting(false);
    }
  };

  if (loading) return null;
  if (!request) return null;

  const fields: Array<{ label: string; value: string; ciId?: number }> = [
    { label: '成本中心 / 费用归属', value: request.costCenter || '-' },
    { label: '数据安全等级', value: request.dataClassification || '-' },
    { label: '申请数量', value: request.quantity ? `${request.quantity} 台` : '1' },
    { label: '需要公网 IP', value: request.needsPublicIp ? '是' : '否' },
    { label: '源 IP 白名单', value: request.sourceIpWhitelist || '-' },
    { label: '到期时间', value: request.expireAt ? new Date(request.expireAt).toLocaleString() : '-' },
    { label: '联系人', value: request.contactName || '-' },
    { label: '联系邮箱', value: request.contactEmail || '-' },
    {
      label: '期望交付时间',
      value: request.expectedAt ? new Date(request.expectedAt).toLocaleString() : '-',
    },
    { label: '关联 CI', value: request.ciId ? `CI #${request.ciId}` : '-', ciId: request.ciId },
  ];

  const taskStatusBadge = (task: ProvisioningTask) => {
    if (task.status === 'succeeded') {
      return (
        <span className="text-[11px] text-slate-600 bg-slate-100 border border-slate-200 px-2 py-0.5 rounded font-medium">
          已完成
        </span>
      );
    }
    if (task.status === 'running' || task.status === 'pending') {
      return (
        <span className="text-[11px] text-orange-600 bg-orange-50 border border-orange-200 px-2 py-0.5 rounded font-medium flex items-center gap-1">
          <span className="w-1.5 h-1.5 rounded-full bg-orange-500 animate-ping" /> 执行中
        </span>
      );
    }
    return (
      <span className="text-[11px] text-slate-600 bg-slate-100 border border-slate-200 px-2 py-0.5 rounded font-medium">
        {task.status || '-'}
      </span>
    );
  };

  return (
    <div className="space-y-4">
      {/* 面板头部：标题 + 服务项名 + 常驻开始交付按钮 */}
      <div className="flex items-center justify-between gap-3 border-b border-slate-100 pb-3">
        <div className="flex items-center gap-2 min-w-0">
          <div className="w-6 h-6 rounded-md bg-orange-50 text-orange-600 flex items-center justify-center font-bold text-xs shrink-0">
            ☁️
          </div>
          <span className="font-bold text-sm text-slate-800 shrink-0">服务申请与规格参数</span>
          <span className="text-xs text-orange-700 bg-orange-50 px-2 py-0.5 rounded font-medium border border-orange-200 truncate">
            {request.serviceName || '服务目录申请'}
          </span>
        </div>

        <Button
          type="primary"
          icon={<PlayCircle size={14} />}
          loading={starting}
          onClick={handleStartProvisioning}
          disabled={!request.actions?.provision?.allowed}
          title={request.actions?.provision?.reason || ''}
          className="!bg-orange-500 hover:!bg-orange-600 active:!bg-orange-700 !border-orange-500 hover:!border-orange-600 shrink-0"
        >
          开始交付
        </Button>
      </div>

      {/* 规格字段网格 */}
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 text-xs">
        {fields.map(field => (
          <div key={field.label} className="p-3 bg-slate-50 rounded-xl border border-slate-100 space-y-1">
            <span className="text-slate-400 block text-[11px]">{field.label}</span>
            {field.ciId ? (
              <button
                type="button"
                onClick={() => router.push(`/cmdb/cis/${field.ciId}`)}
                className="font-semibold text-orange-600 hover:text-orange-700 text-xs inline-flex items-center gap-1 cursor-pointer"
              >
                {field.value}
                <ExternalLink size={11} />
              </button>
            ) : (
              <span className="font-semibold text-slate-800 block text-xs break-words">{field.value}</span>
            )}
          </div>
        ))}
      </div>

      {/* 交付任务列表 */}
      <div className="pt-2">
        <span className="text-xs font-bold text-slate-700 mb-2 block">资源交付任务 ({tasks.length})</span>
        {tasks.length === 0 ? (
          <Empty description="尚未开始交付" />
        ) : (
          <div className="space-y-2">
            {tasks.map(task => (
              <div
                key={task.id}
                className="flex items-center justify-between gap-3 p-2.5 bg-slate-50/90 rounded-lg border border-slate-100 text-xs"
              >
                <div className="flex items-center gap-2 min-w-0">
                  <span className="font-mono text-slate-400 text-xs shrink-0">#{task.id}</span>
                  <span className="font-medium text-slate-700 text-xs truncate">{task.resourceType || '-'}</span>
                  <span className="text-[11px] text-slate-400 shrink-0">({task.provider || '-'})</span>
                </div>
                <div className="flex items-center gap-3 shrink-0">
                  <span className="text-[11px] text-slate-400 font-mono">
                    {task.updatedAt ? new Date(task.updatedAt).toLocaleString() : '-'}
                  </span>
                  {taskStatusBadge(task)}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

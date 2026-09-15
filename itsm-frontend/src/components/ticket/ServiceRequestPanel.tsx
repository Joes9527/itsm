'use client';

import React, { useEffect, useRef, useState } from 'react';
import { useDetailResource, useDetailIdentity } from '@/components/business/detail-tabs/useDetailResource';
import { useDetailRefreshEntry } from '@/components/business/detail-tabs/DetailRefreshContext';
import { DetailReadState } from '@/components/business/detail-tabs/DetailReadState';

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
export default function ServiceRequestPanel(props: ServiceRequestPanelProps) {
  const identity = useDetailIdentity(props.ticketId);
  return <ServiceRequestPanelContent key={identity} {...props} />;
}
function ServiceRequestPanelContent({ ticketId }: ServiceRequestPanelProps) {
  const router = useRouter();
  const [starting, setStarting] = useState(false);
  const busy = useRef(false);
  const resource = useDetailResource(ticketId, async () => {
    const request = await ServiceCatalogApi.getServiceRequestByTicketId(ticketId);
    const tasks = request?.id ? await serviceRequestAPI.listProvisioningTasks(request.id) : [];
    return { request, tasks: tasks || [] };
  }, data => data.tasks.length);
  useDetailRefreshEntry(!resource.denied ? { key: 'service-request', label: '服务申请', reload: resource.reload, isWriting: () => busy.current } : undefined);
  const request = resource.data?.request;
  const tasks = resource.data?.tasks || [];
  useEffect(() => { if (resource.denied) { busy.current = false; setStarting(false); } }, [resource.denied]);
  const handleStartProvisioning = async () => {
    if (!resource.ready || !request?.id || !request.actions?.provision?.allowed || busy.current) return;
    const current = resource.capture();
    busy.current = true;
    setStarting(true);
    try {
      await serviceRequestAPI.startProvisioning(request.id);
      if (!current()) return;
      message.success('已开始交付');
      await resource.reload({ afterWrite: true });
    } catch (error) {
      if (!current()) return;
      resource.deny(error);
      message.error(error instanceof Error ? error.message : '启动交付失败');
    } finally {
      if (current()) { busy.current = false; setStarting(false); }
    }
  };
  const feedback = <DetailReadState error={resource.error} loading={resource.loading} reload={resource.reload} />;
  if (!request) return resource.error ? feedback : null;

  const fulfillmentLabels: Record<string, string> = {
    awaiting_approval: '待审批',
    fulfilling: '履约中',
    unknown: '结果未知',
    completed: '已完成',
    rejected: '已拒绝',
    cancelled: '已取消',
  };
  const fulfillmentLabel = request.fulfillmentState
    ? fulfillmentLabels[request.fulfillmentState] || '结果未知'
    : null;

  const fields: Array<{ label: string; value: string; ciId?: number }> = [
    { label: '成本中心 / 费用归属', value: request.costCenter || '-' },
    { label: '数据安全等级', value: request.dataClassification || '-' },
    { label: '申请数量', value: request.quantity ? `${request.quantity} 台` : '1' },
    { label: '需要公网 IP', value: request.needsPublicIp ? '是' : '否' },
    { label: '源 IP 白名单', value: request.sourceIpWhitelist || '-' },
    {
      label: '到期时间',
      value: request.expireAt ? new Date(request.expireAt).toLocaleString() : '-',
    },
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
        <span className="text-[11px] text-muted bg-raised border border-border px-2 py-0.5 rounded font-medium">
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
      <span className="text-[11px] text-muted bg-raised border border-border px-2 py-0.5 rounded font-medium">
        {task.status || '-'}
      </span>
    );
  };

  return (
    <div className="space-y-4">
      {feedback}
      {/* 面板头部：标题 + 服务项名 + 常驻开始交付按钮 */}
      <div className="flex items-center justify-between gap-3 border-b border-border pb-3">
        <div className="flex items-center gap-2 min-w-0">
          <div className="w-6 h-6 rounded-md bg-orange-50 text-orange-600 flex items-center justify-center font-bold text-[12px] shrink-0">
            ☁️
          </div>
          <span className="font-semibold text-[15px] text-foreground shrink-0">
            服务申请与规格参数
          </span>
          <span className="text-[12px] text-orange-700 bg-orange-50 px-2 py-0.5 rounded font-medium border border-orange-200 truncate">
            {request.serviceName || '服务目录申请'}
          </span>
        </div>

        {!fulfillmentLabel && (
          <Button
            type="primary"
            icon={<PlayCircle size={14} />}
            loading={starting}
            onClick={handleStartProvisioning}
            disabled={!request.actions?.provision?.allowed}
            title={request.actions?.provision?.reason || ''}
            className="shrink-0"
          >
            开始交付
          </Button>
        )}
      </div>

      {fulfillmentLabel && (
        <div role="status" className="rounded-[8px] border border-border p-3 text-[13px]">
          <strong>{fulfillmentLabel}</strong>
          {request.fulfillmentState === 'unknown' && <p>执行结果待核查，请联系服务团队。</p>}
          {request.accessResult && (
            <div>
              <p>
                {request.accessResult.outcome === 'already_present' ? '权限已存在' : '授权已验证'}
              </p>
              <p>验证时间：{new Date(request.accessResult.verifiedAt).toLocaleString()}</p>
              {request.accessResult.expiresAt && (
                <p>申请有效期至：{new Date(request.accessResult.expiresAt).toLocaleString()}</p>
              )}
            </div>
          )}
        </div>
      )}

      {/* 规格字段网格 */}
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 text-[12px]">
        {fields.map(field => (
          <div
            key={field.label}
            className="p-3 bg-raised rounded-[8px] border border-border space-y-1"
          >
            <span className="text-muted block text-[11px]">
              {field.label}
            </span>
            {field.ciId ? (
              <button
                type="button"
                onClick={() => router.push(`/cmdb/cis/${field.ciId}`)}
                className="font-semibold text-orange-600 hover:text-orange-700 text-[12px] inline-flex items-center gap-1 cursor-pointer"
              >
                {field.value}
                <ExternalLink size={11} />
              </button>
            ) : (
              <span className="font-semibold text-foreground block text-[13px] break-words">
                {field.value}
              </span>
            )}
          </div>
        ))}
      </div>

      {/* 交付任务列表 */}
      {!fulfillmentLabel && (
        <div className="pt-2">
          <span className="text-[15px] font-semibold text-foreground mb-2 block">
            资源交付任务 ({tasks.length})
          </span>
          {tasks.length === 0 ? (
            <Empty description="尚未开始交付" />
          ) : (
            <div className="space-y-2">
              {tasks.map(task => (
                <div
                  key={task.id}
                  className="flex items-center justify-between gap-3 p-2.5 bg-raised rounded-[8px] border border-border text-[12px]"
                >
                  <div className="flex items-center gap-2 min-w-0">
                    <span className="font-mono text-muted text-[12px] shrink-0">
                      #{task.id}
                    </span>
                    <span className="font-medium text-foreground text-[12px] truncate">
                      {task.resourceType || '-'}
                    </span>
                    <span className="text-[11px] text-muted shrink-0">
                      ({task.provider || '-'})
                    </span>
                  </div>
                  <div className="flex items-center gap-3 shrink-0">
                    <span className="text-[11px] text-muted font-mono">
                      {task.updatedAt ? new Date(task.updatedAt).toLocaleString() : '-'}
                    </span>
                    {taskStatusBadge(task)}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

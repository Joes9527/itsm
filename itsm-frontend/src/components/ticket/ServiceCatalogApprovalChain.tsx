'use client';

import React from 'react';
import { useDetailResource } from '@/components/business/detail-tabs/useDetailResource';
import { useDetailRefreshEntry } from '@/components/business/detail-tabs/DetailRefreshContext';
import { DetailReadState } from '@/components/business/detail-tabs/DetailReadState';

import { Card, Tag, Typography } from 'antd';
import { GitBranch } from 'lucide-react';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';

const { Text } = Typography;

interface ServiceCatalogApprovalChainProps {
  ticketId: number;
}

/**
 * 展示服务目录来源工单的审批链步骤——从 ServiceRequest.formData._approval_chain 读取，
 * 显示在 TicketDetail 的「审批链」Tab 中。
 */
export default function ServiceCatalogApprovalChain({ ticketId }: ServiceCatalogApprovalChainProps) {
  const resource = useDetailResource(ticketId, async () => {
    const sr = await ServiceCatalogApi.getServiceRequestByTicketId(ticketId);
    const chain = sr?.formData?.ApprovalChain || sr?.formData?._approval_chain;
    return Array.isArray(chain) ? chain : [];
  }, steps => steps.length);
  useDetailRefreshEntry({ key: 'catalog-approval-chain', label: '目录审批链', reload: resource.reload, isWriting: () => false });
  const steps = resource.data || [];
  const feedback = <DetailReadState error={resource.error} loading={resource.loading} reload={resource.reload} />;
  if (!steps.length) {
    // 空数组和读取失败是两回事：过去这里只渲染一个刷新按钮，用户看不出"这个服务申请
    // 本来就没有预解析审批链"。有错误时 DetailReadState 已经给出了原因，不叠加猜测。
    return (
      <div>
        {feedback}
        {resource.ready && !resource.error && !resource.loading && (
          <p className="text-xs text-muted mb-4">
            本服务申请提交时未匹配到审批链规则，因此没有预解析的审批步骤；实际审批以流程任务为准。
          </p>
        )}
      </div>
    );
  }

  return (
    <Card
      size="small"
      title={
        <span>
          <GitBranch size={14} className="inline mr-1" />
          审批链（预解析）
        </span>
      }
      style={{ marginBottom: 16 }}
    >
      {feedback}
      <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 8 }}>
        {steps.map((step: any, idx: number) => (
          <React.Fragment key={idx}>
            {idx > 0 && <Text type="secondary">→</Text>}
            <Tag color="blue" style={{ fontSize: 13, padding: '4px 8px' }}>
              L{step.level}: {step.name}
              <Text type="secondary" style={{ marginLeft: 4, fontSize: 12 }}>
                ({step.role}{step.approval_type === 'parallel' ? '·会签' : ''})
              </Text>
            </Tag>
          </React.Fragment>
        ))}
      </div>
      <Text type="secondary" style={{ display: 'block', marginTop: 8, fontSize: 12 }}>
        以上步骤在服务申请提交时根据审批链规则自动解析，实际执行以 BPMN 流程为准。
      </Text>
    </Card>
  );
}

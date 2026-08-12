'use client';

import React, { useEffect, useState } from 'react';
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
  const [steps, setSteps] = useState<any[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    ServiceCatalogApi.getServiceRequestByTicketId(ticketId)
      .then((sr) => {
        if (cancelled) return;
        // http-client 的 toCamelCase 会把 _approval_chain 转为 ApprovalChain
        const chain = sr?.formData?.ApprovalChain || sr?.formData?._approval_chain;
        if (Array.isArray(chain) && chain.length > 0) {
          setSteps(chain);
        }
      })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [ticketId]);

  if (!steps || steps.length === 0) return null;

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

'use client';

import React, { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { Card, Descriptions, Tag, Table, Button, message, Empty } from 'antd';
import { PlayCircle } from 'lucide-react';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { serviceRequestAPI } from '@/lib/api/service-request-api';
import type { ProvisioningTask } from '@/lib/api/service-request-api';

interface ServiceRequestPanelProps {
  ticketId: number;
}

// 服务目录来源的工单，在工单详情页里额外展示的补充信息面板——
// 之前分散在两个独立详情页（/service-requests/[id]、/my-requests/[requestId]）里的
// SR 专属字段和交付任务展示，这次统一收到这里，不再维护两份重复代码。
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

  return (
    <Card title="服务申请信息" style={{ marginBottom: 16 }}>
      <Descriptions column={2} bordered size="small">
        <Descriptions.Item label="联系人">{request.contactName || '-'}</Descriptions.Item>
        <Descriptions.Item label="联系邮箱">{request.contactEmail || '-'}</Descriptions.Item>
        <Descriptions.Item label="数量">{request.quantity || 1}</Descriptions.Item>
        <Descriptions.Item label="期望交付时间">
          {request.expectedAt ? new Date(request.expectedAt).toLocaleString() : '-'}
        </Descriptions.Item>
        <Descriptions.Item label="成本中心">{request.costCenter || '-'}</Descriptions.Item>
        <Descriptions.Item label="数据分类">{request.dataClassification || 'internal'}</Descriptions.Item>
        <Descriptions.Item label="需要公网IP">{request.needsPublicIp ? '是' : '否'}</Descriptions.Item>
        <Descriptions.Item label="到期时间">
          {request.expireAt ? new Date(request.expireAt).toLocaleString() : '-'}
        </Descriptions.Item>
        <Descriptions.Item label="关联CI">
          {request.ciId ? (
            <Button type="link" onClick={() => router.push(`/cmdb/cis/${request.ciId}`)}>
              CI #{request.ciId}
            </Button>
          ) : (
            '-'
          )}
        </Descriptions.Item>
      </Descriptions>

      <Card type="inner" title="资源交付" style={{ marginTop: 16 }}>
        {tasks.length === 0 ? (
          <Empty description="尚未开始交付">
            <Button
              type="primary"
              icon={<PlayCircle size={14} />}
              loading={starting}
              onClick={handleStartProvisioning}
            >
              开始交付
            </Button>
          </Empty>
        ) : (
          <Table
            size="small"
            rowKey="id"
            dataSource={tasks}
            pagination={false}
            columns={[
              { title: 'Provider', dataIndex: 'provider' },
              { title: '资源类型', dataIndex: 'resourceType' },
              {
                title: '状态',
                dataIndex: 'status',
                render: (s: string) => <Tag>{s}</Tag>,
              },
              { title: '更新时间', dataIndex: 'updatedAt', render: (t: string) => new Date(t).toLocaleString() },
            ]}
          />
        )}
      </Card>
    </Card>
  );
}

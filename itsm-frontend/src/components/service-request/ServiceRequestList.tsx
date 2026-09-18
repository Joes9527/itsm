'use client';

/**
 * 服务请求列表组件——"我的请求"
 *
 * 服务请求域只展示请求记录；审批由关联 WorkItem 的 BPMN ProcessTask
 * 唯一承载，并在 /approvals 中展示。这里不再定义第二套待审数据源或阶段。
 */

import React, { useState, useEffect } from 'react';
import { Table, Tag, Button, Card, Space, Tooltip, message, Empty } from 'antd';
import {
  EyeOutlined,
  SyncOutlined,
} from '@ant-design/icons';
import { useRouter } from 'next/navigation';
import dayjs from 'dayjs';

import { serviceRequestAPI } from '@/lib/api/service-request-api';
import { ServiceRequestStatus } from '@/constants/service-request';
import type { ServiceRequest, ServiceRequestQuery } from '@/types/biz/service-request';

// 状态标签颜色映射
const statusColors: Record<string, string> = {
  [ServiceRequestStatus.SUBMITTED]: 'blue',
  [ServiceRequestStatus.MANAGER_APPROVED]: 'cyan',
  [ServiceRequestStatus.IT_APPROVED]: 'geekblue',
  [ServiceRequestStatus.SECURITY_APPROVED]: 'purple',
  [ServiceRequestStatus.PROVISIONING]: 'processing',
  [ServiceRequestStatus.DELIVERED]: 'green',
  [ServiceRequestStatus.FAILED]: 'red',
  [ServiceRequestStatus.REJECTED]: 'red',
  [ServiceRequestStatus.CANCELLED]: 'default',
};

const ServiceRequestList: React.FC = () => {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [data, setData] = useState<ServiceRequest[]>([]);
  const [total, setTotal] = useState(0);

  // 查询状态
  const [query, setQuery] = useState<ServiceRequestQuery>({
    page: 1,
    size: 10,
    scope: 'me',
  });

  // 加载数据——只有"我的请求"这一个视图了
  const loadData = async () => {
    setLoading(true);
    try {
      const resp = await serviceRequestAPI.getUserServiceRequests({
        page: query.page,
        size: query.size,
        status: query.status,
      });
      setData((resp.requests || []) as unknown as ServiceRequest[]);
      setTotal(resp?.total ?? 0);
    } catch (error) {
      // console.error(error);
      message.error('加载服务请求失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadData();
  }, [query]);

  // 表格列定义
  // 状态/标题已经委托给关联 Ticket——用后端批量回填的 ticketTitle/ticketStatus 展示，
  // 详情/审批都跳转到 /tickets/:ticketId（服务请求已经没有独立详情页）。
  const columns = [
    {
      title: 'ID',
      dataIndex: 'id',
      width: 80,
    },
    {
      title: '标题',
      dataIndex: 'ticketTitle',
      render: (text: string, record: ServiceRequest) => (
        <div className="flex flex-col">
          <span className="font-medium text-foreground">
            {text || `请求 #${record.id}`}
          </span>
          <span className="text-[12px] text-muted">
            {record.catalog?.name || '未知服务'}
          </span>
        </div>
      ),
    },
    {
      title: '状态',
      dataIndex: 'ticketStatus',
      width: 150,
      render: (status: string) =>
        status ? <Tag color={statusColors[status] || 'default'}>{status}</Tag> : '-',
    },
    {
      title: '提交时间',
      dataIndex: 'createdAt',
      width: 180,
      render: (date: string) => dayjs(date).format('YYYY-MM-DD HH:mm'),
      responsive: ['sm'],
    },
    {
      title: '操作',
      key: 'action',
      width: 120,
      render: (_: unknown, record: ServiceRequest) => (
        <Space size="small">
          <Tooltip title="查看详情">
            <Button aria-label="查看详情"
              type="text"
              icon={<EyeOutlined aria-hidden="true" />}
              className="text-foreground hover:!text-foreground hover:!bg-raised"
              onClick={() => router.push(`/tickets/${record.ticketId}`)}
            />
          </Tooltip>
        </Space>
      ),
    },
  ];

  return (
    <Card className="rounded-[8px] shadow-none border border-border">
      <div className="flex justify-between items-center mb-4">
        <h3 className="text-[15px] font-semibold text-foreground">我的请求</h3>
        <Button icon={<SyncOutlined aria-hidden="true" />} onClick={loadData}>
          刷新
        </Button>
      </div>

      <Table
        rowKey="id"
        columns={columns as any}
        dataSource={data}
        loading={loading}
        scroll={{ x: 'max-content' }}
        locale={{
          emptyText: (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无服务请求数据">
              <Button type="primary" onClick={() => router.push('/service-requests/new')}>
                创建第一个服务请求
              </Button>
            </Empty>
          ),
        }}
        pagination={{
          current: query.page,
          pageSize: query.size,
          total: total,
          onChange: (page, size) => setQuery(prev => ({ ...prev, page, size })),
          showSizeChanger: true,
          showQuickJumper: true,
          showTotal: total => `共 ${total} 条记录`,
          pageSizeOptions: ['10', '20', '50', '100'],
        }}
      />
    </Card>
  );
};

export default ServiceRequestList;

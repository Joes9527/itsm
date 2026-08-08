'use client';

/**
 * 服务请求列表组件——"我的请求"
 *
 * 原来还有一个内部"待办审批" Tab，数据源 ServiceRequestApi.getPendingApprovals 打在
 * Task 1 已经删除的 /api/v1/service-requests/approvals/pending 上（SR 自己的审批阶段
 * 概念整体退休，审批现在走关联 Ticket 自己的 BPMN 流程，对应视图见
 * /approvals/pending 页面的"我作为候选组员（BPMN）"Tab）。与其保留一个数据源已经不存在、
 * 靠 catch 掩盖成永远空的 Tab，不如去掉——只保留"我的请求"这一个真实存在的视图。
 */

import React, { useState, useEffect } from 'react';
import { Table, Tag, Button, Card, Space, Tooltip, message, Empty } from 'antd';
import { Eye, RefreshCw } from 'lucide-react';
import { useRouter } from 'next/navigation';
import dayjs from 'dayjs';

import { ServiceRequestApi } from '@/lib/api/';
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
      const resp = await ServiceRequestApi.getServiceRequests({
        ...query,
        scope: 'me',
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
          <span className="font-medium text-gray-900">{text || `请求 #${record.id}`}</span>
          <span className="text-xs text-gray-500">{record.catalog?.name || '未知服务'}</span>
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
            <Button
              type="text"
              icon={<Eye />}
              className="text-blue-600 hover:text-blue-800 hover:bg-blue-50"
              onClick={() => router.push(`/tickets/${record.ticketId}`)}
            />
          </Tooltip>
        </Space>
      ),
    },
  ];

  return (
    <Card className="rounded-lg shadow-sm border border-gray-200">
      <div className="flex justify-between items-center mb-4">
        <h3 className="text-base font-medium text-gray-900">我的请求</h3>
        <Button icon={<RefreshCw />} onClick={loadData}>
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

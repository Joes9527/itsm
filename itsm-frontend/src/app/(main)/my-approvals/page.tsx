'use client';

import React, { useState, useEffect } from 'react';
import { Card, Table, Tag, Typography, Button, Space, message } from 'antd';
import { useRouter } from 'next/navigation';
import { httpClient } from '@/lib/api/http-client';

const { Title } = Typography;

interface ApprovalTask {
  id: number;
  taskId: string;
  taskDefinitionKey: string;
  taskName: string;
  taskType: string;
  status: string;
  priority: string;
  processInstanceId: number;
  processDefinitionKey: string;
  businessKey: string;
  businessType: string;
  businessId: number;
  taskPurpose: string;
  createdTime: string;
  dueDate?: string;
}

interface PagedResponse {
  items: ApprovalTask[];
  page: number;
  page_size: number;
  total: number;
}

const statusColors: Record<string, string> = {
  pending: 'orange',
  assigned: 'blue',
  completed: 'green',
  cancelled: 'default',
};

export default function MyApprovalsPage() {
  const router = useRouter();
  const [tasks, setTasks] = useState<ApprovalTask[]>([]);
  const [loading, setLoading] = useState(false);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);

  const fetchTasks = async (p: number = 1) => {
    setLoading(true);
    try {
      const res = await httpClient.get<PagedResponse>('/api/v1/my-approvals', {
        page: p,
        pageSize: 20,
      });
      setTasks(res.items || []);
      setTotal(res.total || 0);
      setPage(p);
    } catch {
      message.error('加载待审批列表失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchTasks();
  }, []);

  const columns = [
    {
      title: '任务名称',
      dataIndex: 'taskName',
      key: 'taskName',
      width: 180,
    },
    {
      title: '类型',
      dataIndex: 'taskPurpose',
      key: 'taskPurpose',
      width: 80,
      render: (v: string) => {
        const label: Record<string, string> = { approval: '审批', review: '复核' };
        return <Tag>{label[v] || v || '任务'}</Tag>;
      },
    },
    {
      title: '来源',
      key: 'source',
      width: 200,
      render: (_: unknown, r: ApprovalTask) => (
        <span>
          {r.processDefinitionKey} / {r.businessKey || `${r.businessType}:${r.businessId}`}
        </span>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 80,
      render: (v: string) => <Tag color={statusColors[v] || 'default'}>{v}</Tag>,
    },
    {
      title: '创建时间',
      dataIndex: 'createdTime',
      key: 'createdTime',
      width: 170,
      render: (v: string) => new Date(v).toLocaleString('zh-CN'),
    },
    {
      title: '操作',
      key: 'action',
      width: 150,
      render: (_: unknown, r: ApprovalTask) => (
        <Space>
          <Button
            type="link"
            size="small"
            onClick={() => router.push(`/tickets/${r.businessId}`)}
          >
            查看
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <div style={{ padding: 24 }}>
      <Title level={4} style={{ marginBottom: 16 }}>待审批</Title>
      <Card>
        <Table
          rowKey="id"
          columns={columns}
          dataSource={tasks}
          loading={loading}
          pagination={{
            current: page,
            total,
            pageSize: 20,
            showTotal: (t: number) => `共 ${t} 项`,
            onChange: (p: number) => fetchTasks(p),
          }}
          locale={{ emptyText: '暂无待审批任务' }}
        />
      </Card>
    </div>
  );
}

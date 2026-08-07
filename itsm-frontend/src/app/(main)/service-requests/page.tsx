'use client';

import React, { useState, useEffect } from 'react';
import { Card, Row, Col, Statistic, Typography } from 'antd';
import ServiceRequestList from '@/components/service-request/ServiceRequestList';
import { FileText, Clock, CheckCircle, Inbox } from 'lucide-react';
import { serviceRequestAPI } from '@/lib/api/service-request-api';

const { Title, Text } = Typography;

export default function ServiceRequestsPage() {
  const [stats, setStats] = useState({
    totalRequests: 0,
    pending: 0,
    processing: 0,
    completed: 0,
  });

  // Fetch stats
  //
  // 状态/标题已经全部委托给关联的 Ticket（Task 1 从 ServiceRequest 表移除了 status/title/
  // reason）。列表接口按 handlers/service_request/service.go List 的批量回填，每条记录带上
  // 关联 ticket 的 ticketStatus（值域见 src/types/ticket.ts 的 TicketStatus：
  // new/open/in_progress/pending/resolved/closed/cancelled），不再是 SR 自己的审批阶段
  // （submitted/manager_approved/it_approved/security_approved/provisioning/delivered，
  // 这些字符串已经不存在于响应里）。这里按 ticketStatus 重新分桶：pending≈尚未开始处理，
  // processing=处理中，completed=已解决/已关闭。cancelled 不计入任何桶，和旧逻辑里
  // rejected/cancelled/failed 也不计入是一致的。
  const fetchStats = async () => {
    try {
      const allRequests = await serviceRequestAPI
        .getUserServiceRequests({ page: 1, size: 100 })
        .catch(() => ({ requests: [], total: 0 }));

      const requests = allRequests.requests || [];
      const pending = requests.filter(
        (r: any) =>
          r.ticketStatus === 'new' || r.ticketStatus === 'open' || r.ticketStatus === 'pending'
      ).length;
      const processing = requests.filter((r: any) => r.ticketStatus === 'in_progress').length;
      const completed = requests.filter(
        (r: any) => r.ticketStatus === 'resolved' || r.ticketStatus === 'closed'
      ).length;

      setStats({
        totalRequests: allRequests.total || 0,
        pending,
        processing,
        completed,
      });
    } catch (error) {
      console.error('Failed to fetch service request stats:', error);
    }
  };

  useEffect(() => {
    fetchStats();
  }, []);

  return (
    <div className="p-6 min-h-screen bg-gray-50">
      {/* 页面头部 */}
      <div className="mb-6 flex justify-between items-center">
        <div>
          <Title level={2} style={{ marginBottom: 4 }}>
            服务请求
          </Title>
          <Text type="secondary">
            查看和管理服务请求及审批流程
          </Text>
        </div>
      </div>

      {/* 统计卡片 */}
      <Row gutter={[16, 16]} className="mb-6">
        <Col xs={24} sm={12} lg={6}>
          <Card className="rounded-lg shadow-sm">
            <Statistic
              title="请求总数"
              value={stats.totalRequests}
              prefix={<FileText className="text-blue-500 mr-2" />}
              styles={{ content: { color: '#1890ff' } }}
            />
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card className="rounded-lg shadow-sm">
            {/* SR 自己的审批阶段概念已经在 Task 1 退休，这里按关联 ticket 的状态统计
                "尚未开始处理"的请求数，标题相应改成"待处理"而不是"待审批"。 */}
            <Statistic
              title="待处理"
              value={stats.pending}
              prefix={<Clock className="text-orange-500 mr-2" />}
              styles={{ content: { color: '#fa8c16' } }}
            />
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card className="rounded-lg shadow-sm">
            <Statistic
              title="处理中"
              value={stats.processing}
              prefix={<Inbox className="text-blue-500 mr-2" />}
              styles={{ content: { color: '#1890ff' } }}
            />
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card className="rounded-lg shadow-sm">
            <Statistic
              title="已完成"
              value={stats.completed}
              prefix={<CheckCircle className="text-green-500 mr-2" />}
              styles={{ content: { color: '#52c41a' } }}
            />
          </Card>
        </Col>
      </Row>

      {/* 主要内容
          原来这里是"我的请求"/"待审批"两个 Tab。"待审批"这个 Tab 承载的是 SR 自己的审批
          阶段（审批人对某条 SR 直接 approve/reject），Task 1 已经把这个概念整体退休——
          审批现在走关联 Ticket 自己的 BPMN 流程，还没有对应的"待我审批的工单任务"视图
          （那是"审批收敛到 BPMN"这条后续工作的范围，不在本次改造内）。与其保留一个
          永远空的 Tab 假装这个能力还在，不如直接去掉；ServiceRequestList 内部自己的
          "待办审批" Tab 是另一个独立的、更早的入口，这里不重复处理。 */}
      <Card>
        <ServiceRequestList />
      </Card>
    </div>
  );
}

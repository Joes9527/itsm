'use client';

import React, { useState, useEffect } from 'react';
import { Card, Row, Col, Typography, Spin, App, Alert, Statistic, Button, Tag, Space } from 'antd';
import {
  PieChart,
  Pie,
  Cell,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
  ResponsiveContainer,
} from 'recharts';
import { SyncOutlined } from '@ant-design/icons';
import { ChangeApi } from '@/lib/api/change-api';

const { Title, Text } = Typography;

const STATUS_COLORS: Record<string, string> = {
  draft: '#d9d9d9',
  pending: '#faad14',
  approved: '#52c41a',
  rejected: '#ff4d4f',
  implementing: '#1890ff',
  completed: '#722ed1',
  cancelled: '#8c8c8c',
};

const TYPE_COLORS = ['#1890ff', '#52c41a', '#faad14'];

interface ChangeData {
  byStatus: { name: string; value: number; color: string }[];

  successRate: number | null;
  totalChanges: number;
}

const ChangeSuccessReport = () => {
  const { message } = App.useApp();
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [data, setData] = useState<ChangeData | null>(null);

  const loadData = async () => {
    setLoading(true);
    try {
      const stats = await ChangeApi.getChangeStats();
      const recorded = stats.successfulOutcomes + stats.failedOutcomes + stats.rolledBackOutcomes;
      setError('');
      setData({
        totalChanges: stats.total,
        successRate: recorded > 0 ? (stats.successfulOutcomes / recorded) * 100 : null,
        byStatus: [
          { name: '草稿', value: stats.draft, color: STATUS_COLORS.draft },
          { name: '待审批', value: stats.pending, color: STATUS_COLORS.pending },
          { name: '已批准', value: stats.approved, color: STATUS_COLORS.approved },
          { name: '已排期', value: stats.scheduled, color: '#13c2c2' },
          { name: '实施中', value: stats.inProgress, color: STATUS_COLORS.implementing },
          { name: '已完成', value: stats.completed, color: STATUS_COLORS.completed },
          { name: '失败状态', value: stats.failed, color: STATUS_COLORS.rejected },
          { name: '已回滚状态', value: stats.rolledBack, color: '#eb2f96' },
          { name: '已拒绝', value: stats.rejected, color: STATUS_COLORS.rejected },
          { name: '已取消', value: stats.cancelled, color: STATUS_COLORS.cancelled },
        ],
      });
    } catch (error) {
      setError(error instanceof Error ? error.message : '加载报表失败');
      message.error('加载数据失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadData();
  }, []);

  const CustomTooltip = ({ active, payload }: any) => {
    if (active && payload && payload.length) {
      return (
        <div className="bg-surface p-3 rounded-[8px] shadow-lg border border-border">
          <p className="font-semibold text-foreground">{`${payload[0].name}`}</p>
          <p
            className="text-[13px]"
            style={{
              color: 'var(--color-text-primary)',
              borderLeft: `3px solid ${payload[0].color}`,
              paddingLeft: 8,
            }}
          >{`数量: ${payload[0].value}`}</p>
        </div>
      );
    }
    return null;
  };

  if (!data) {
    return (
      <div className='p-6 flex items-center justify-center h-64'>
        {error ? (
          <Alert type='error' title={error} action={<Button onClick={loadData}>重试</Button>} />
        ) : (
          <Spin size='large' />
        )}
      </div>
    );
  }

  return (
    <div className="p-[24px] max-[1200px]:p-[16px] bg-page min-h-full">
      {error && <Alert type='error' title={error} />}
      <header className='mb-6'>
        <Title level={2}>变更成功率报表</Title>
        <p className='text-muted mt-1'>
          成功率 = 成功结果数 / 已记录结果数（成功、失败、已回滚）；关闭状态单独统计
        </p>
      </header>

      {/* 控制栏 */}
      <Card className='mb-6'>
        <Row justify='space-between' align='middle'>
          <Col>
            <Text className="text-muted">变更执行情况监控</Text>
          </Col>
          <Col>
            <Button icon={<SyncOutlined aria-hidden="true" />} onClick={loadData}>
              刷新数据
            </Button>
          </Col>
        </Row>
      </Card>

      {loading ? (
        <div className='flex items-center justify-center h-64'>
          <Spin size='large' description='加载报表数据...' />
        </div>
      ) : (
        <>
          {/* 统计卡片 */}
          <Row gutter={[14, 14]} className="mb-6">
            <Col xs={24} sm={12} lg={6}>
              <Card>
                <Statistic
                  title='变更总数'
                  value={data.totalChanges}
                  styles={{ content: { color: '#1890ff' } }}
                />
              </Card>
            </Col>
            <Col xs={24} sm={12} lg={6}>
              <Card>
                <Statistic
                  title='已完成'
                  value={data.byStatus.find(s => s.name === '已完成')?.value || 0}
                  styles={{ content: { color: '#52c41a' } }}
                />
              </Card>
            </Col>
            <Col xs={24} sm={12} lg={6}>
              <Card>
                <Statistic
                  title='实施中'
                  value={data.byStatus.find(s => s.name === '实施中')?.value || 0}
                  styles={{ content: { color: '#1890ff' } }}
                />
              </Card>
            </Col>
            <Col xs={24} sm={12} lg={6}>
              <Card>
                <Statistic
                  title='成功率'
                  value={data.successRate ?? '暂无已记录结果'}
                  suffix={data.successRate === null ? undefined : '%'}
                  styles={{
                    content: { color: (data.successRate ?? 0) >= 80 ? '#52c41a' : '#ff4d4f' },
                  }}
                />
              </Card>
            </Col>
          </Row>

          {/* 图表区域 */}
          <Row gutter={[14, 14]}>
            <Col xs={24} lg={12}>
              <Card title='变更状态分布'>
                <ResponsiveContainer width='100%' height={300}>
                  <PieChart>
                    <Pie
                      data={data.byStatus}
                      cx='50%'
                      cy='50%'
                      outerRadius={100}
                      dataKey='value'
                      nameKey='name'
                      label={({ name, percent }) => `${name} ${((percent || 0) * 100).toFixed(0)}%`}
                    >
                      {data.byStatus.map((entry, index) => (
                        <Cell key={`cell-${index}`} fill={entry.color} />
                      ))}
                    </Pie>
                    <Tooltip content={<CustomTooltip />} />
                    <Legend formatter={value => <span className="text-foreground">{value}</span>} />
                  </PieChart>
                </ResponsiveContainer>
              </Card>
            </Col>
          </Row>

          {/* 状态说明 */}
          <Card title='状态说明' className='mt-6'>
            <Row gutter={[16, 16]}>
              {data.byStatus.map((status, index) => (
                <Col xs={12} sm={8} md={4} key={index}>
                  <div className='flex items-center gap-2'>
                    <Tag color={status.color} className='m-0'>
                      {status.name}
                    </Tag>
                    <span className="text-[15px] font-semibold">{status.value}</span>
                  </div>
                </Col>
              ))}
            </Row>
          </Card>
        </>
      )}
    </div>
  );
};

export default ChangeSuccessReport;

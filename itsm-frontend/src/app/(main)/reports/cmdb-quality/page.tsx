'use client';

import React, { useState, useEffect, useCallback } from 'react';
import { App, Button, Card, Col, Empty, Row, Skeleton, Statistic, Typography } from 'antd';
import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { RotateCcw } from 'lucide-react';

import { CMDBApi } from '@/lib/api/cmdb-api';

const { Title, Text } = Typography;

const COLORS = {
  completeness: '#1890ff',
  accuracy: '#52c41a',
  consistency: '#faad14',
};

interface QualityData {
  name: string;
  completeness: number;
  accuracy: number;
  consistency: number;
}

const CMDBQualityReport = () => {
  const { message } = App.useApp();
  const [loading, setLoading] = useState(false);
  const [qualityData, setQualityData] = useState<QualityData[]>([]);
  const [stats, setStats] = useState<Record<string, unknown> | null>(null);
  const [hasData, setHasData] = useState<boolean>(false);

  const loadData = useCallback(async () => {
    setLoading(true);
    setHasData(false);
    try {
      // 调用真实 CMDB 统计接口
      const result = await CMDBApi.getCMDBStats();
      setStats(result || null);

      const data = Array.isArray(result?.byCategory)
        ? (result.byCategory as QualityData[])
        : Array.isArray(result?.quality)
          ? (result.quality as QualityData[])
          : [];

      if (data.length > 0) {
        setQualityData(data);
        setHasData(true);
      } else {
        setQualityData([]);
        setHasData(false);
      }
    } catch (error) {
      console.warn('获取 CMDB 数据失败:', error);
      message.error('加载CMDB质量数据失败');
      setQualityData([]);
      setStats(null);
    } finally {
      setLoading(false);
    }
  }, [message]);

  useEffect(() => {
    loadData();
  }, [loadData]);

  const avgCompleteness =
    qualityData.length > 0
      ? qualityData.reduce((sum, d) => sum + d.completeness, 0) / qualityData.length
      : 0;
  const avgAccuracy =
    qualityData.length > 0
      ? qualityData.reduce((sum, d) => sum + d.accuracy, 0) / qualityData.length
      : 0;
  const avgConsistency =
    qualityData.length > 0
      ? qualityData.reduce((sum, d) => sum + d.consistency, 0) / qualityData.length
      : 0;

  interface TooltipPayloadEntry {
    color?: string;
    name?: string;
    value?: number | string;
  }

  const CustomTooltip = ({
    active,
    payload,
    label,
  }: {
    active?: boolean;
    payload?: TooltipPayloadEntry[];
    label?: string | number;
  }) => {
    if (active && payload && payload.length) {
      return (
        <div className="bg-surface p-3 rounded-[8px] shadow-lg border border-border">
          <p className="font-semibold text-foreground mb-2">{`分类: ${label ?? ''}`}</p>
          {payload.map((entry, index) => (
            <p
              key={`${entry.name ?? 'item'}-${index}`}
              className="text-[13px]"
              style={{
                color: 'var(--color-text-primary)',
                borderLeft: `3px solid ${entry.color}`,
                paddingLeft: 8,
              }}
            >
              {`${entry.name ?? ''}: ${entry.value ?? 0}%`}
            </p>
          ))}
        </div>
      );
    }
    return null;
  };

  const renderContent = () => {
    if (loading) {
      return (
        <div className="space-y-4">
          <Row gutter={[16, 16]}>
            {[0, 1, 2].map(i => (
              <Col xs={24} sm={8} key={`skeleton-${i}`}>
                <Card>
                  <Skeleton active />
                </Card>
              </Col>
            ))}
          </Row>
          <Skeleton active paragraph={{ rows: 6 }} />
        </div>
      );
    }

    if (!hasData || qualityData.length === 0) {
      return (
        <Card>
          <Empty description="暂无 CMDB 数据质量统计" />
        </Card>
      );
    }

    return (
      <>
        {/* 统计卡片 */}
        <Row gutter={[16, 16]} className="mb-6">
          <Col xs={24} sm={8}>
            <Card>
              <Statistic
                title="平均完整度"
                value={avgCompleteness}
                suffix="%"
                styles={{ content: { color: COLORS.completeness } }}
              />
            </Card>
          </Col>
          <Col xs={24} sm={8}>
            <Card>
              <Statistic
                title="平均准确度"
                value={avgAccuracy}
                suffix="%"
                styles={{ content: { color: COLORS.accuracy } }}
              />
            </Card>
          </Col>
          <Col xs={24} sm={8}>
            <Card>
              <Statistic
                title="平均一致度"
                value={avgConsistency}
                suffix="%"
                styles={{ content: { color: COLORS.consistency } }}
              />
            </Card>
          </Col>
        </Row>

        {/* 质量指标对比图 */}
        <Card title="各类别数据质量对比" className="mb-6">
          <ResponsiveContainer width="100%" height={350}>
            <BarChart data={qualityData}>
              <CartesianGrid stroke="var(--color-border)" strokeDasharray="3 3" />
              <XAxis
                stroke="var(--color-text-secondary)"
                tick={{ fill: 'var(--color-text-secondary)', fontSize: 12 }}
                dataKey="name"
              />
              <YAxis
                stroke="var(--color-text-secondary)"
                tick={{ fill: 'var(--color-text-secondary)', fontSize: 12 }}
                domain={[0, 100]}
              />
              <Tooltip content={<CustomTooltip />} />
              <Legend formatter={value => <span className="text-foreground">{value}</span>} />
              <Bar dataKey="completeness" name="完整度" fill={COLORS.completeness} />
              <Bar dataKey="accuracy" name="准确度" fill={COLORS.accuracy} />
              <Bar dataKey="consistency" name="一致度" fill={COLORS.consistency} />
            </BarChart>
          </ResponsiveContainer>
        </Card>

        {/* 质量趋势 */}
        <Card title="数据质量趋势">
          <ResponsiveContainer width="100%" height={300}>
            <LineChart data={qualityData}>
              <CartesianGrid stroke="var(--color-border)" strokeDasharray="3 3" />
              <XAxis
                stroke="var(--color-text-secondary)"
                tick={{ fill: 'var(--color-text-secondary)', fontSize: 12 }}
                dataKey="name"
              />
              <YAxis
                stroke="var(--color-text-secondary)"
                tick={{ fill: 'var(--color-text-secondary)', fontSize: 12 }}
                domain={[0, 100]}
              />
              <Tooltip content={<CustomTooltip />} />
              <Legend formatter={value => <span className="text-foreground">{value}</span>} />
              <Line
                type="monotone"
                dataKey="completeness"
                name="完整度"
                stroke={COLORS.completeness}
                strokeWidth={2}
              />
              <Line
                type="monotone"
                dataKey="accuracy"
                name="准确度"
                stroke={COLORS.accuracy}
                strokeWidth={2}
              />
              <Line
                type="monotone"
                dataKey="consistency"
                name="一致度"
                stroke={COLORS.consistency}
                strokeWidth={2}
              />
            </LineChart>
          </ResponsiveContainer>
        </Card>
      </>
    );
  };

  return (
    <div className="p-[24px] max-[1200px]:p-[16px] bg-page min-h-full">
      <header className="mb-6">
        <Title level={2}>CMDB数据质量报表</Title>
        <p className="text-muted mt-1">展示配置管理数据库的数据质量指标</p>
      </header>

      {/* 控制栏 */}
      <Card className="mb-6">
        <Row justify="space-between" align="middle">
          <Col>
            <Text className="text-muted">配置项数据质量监控</Text>
          </Col>
          <Col>
            <Button icon={<RotateCcw />} onClick={loadData} loading={loading}>
              刷新数据
            </Button>
          </Col>
        </Row>
      </Card>

      {renderContent()}
    </div>
  );
};

export default CMDBQualityReport;

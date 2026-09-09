'use client';

import React from 'react';
import { useTheme } from '@/lib/design-system/theme';
import { theme } from 'antd';
import { Column } from '@ant-design/charts';
import { Smile } from 'lucide-react';
import { DashboardChartCard } from './DashboardChartCard';

interface SatisfactionData {
  month: string;
  rating: number;
  responses: number;
}

const UserSatisfactionChart: React.FC<{ data: SatisfactionData[] }> = React.memo(({ data }) => {
  const { isDark } = useTheme();
  const { token } = theme.useToken();
  const config = {
    theme: isDark ? 'classicDark' : 'classic',
    data: data.map(item => ({
      month: item.month,
      rating: item.rating,
      responses: item.responses,
    })),
    xField: 'month',
    yField: 'rating',
    meta: {
      rating: {
        alias: '满意度评分',
        min: 0,
        max: 5,
      },
    },
    color: '#ec4899',
    columnStyle: {
      radius: [8, 8, 0, 0],
      fill: 'l(270) 0:#ec4899 1:#db2777',
    },
    columnWidthRatio: 0.5,
    label: {
      position: 'top' as const,
      style: {
        fill: '#ec4899',
        fontSize: 13,
        fontWeight: 'bold',
      },
      formatter: (datum: { rating: number }) => `${datum.rating}`,
    },
    animation: {
      appear: {
        duration: 1200,
      },
    },
    tooltip: {
      formatter: (datum: { rating: number; responses: number }) => ({
        name: '满意度评分',
        value: `${datum.rating}/5.0 (${datum.responses}份反馈)`,
      }),
    },
    responsive: true,
  };

  const avgRating =
    data.length > 0 ? data.reduce((sum, item) => sum + item.rating, 0) / data.length : 0;
  const totalResponses = data.reduce((sum, item) => sum + item.responses, 0);
  const trend = data.length > 1 ? (data[data.length - 1].rating / data[0].rating - 1) * 100 : 0;

  return (
    <DashboardChartCard
      title="用户满意度趋势"
      subtitle="月度服务满意度评分变化"
      icon={<Smile style={{ width: 20, height: 20 }} />}
      iconColor="#ec4899"
      trend={{ value: trend, isPositive: trend > 0 }}
      extra={
        <div style={{ textAlign: 'right' }}>
          <div style={{ fontSize: 12, color: token.colorTextSecondary }}>平均评分</div>
          <div style={{ fontSize: 16, fontWeight: 600, color: '#eb2f96' }}>
            {avgRating.toFixed(1)}/5.0
          </div>
        </div>
      }
    >
      <div style={{ height: '280px' }}>
        <Column {...config} />
      </div>

      {/* 满意度统计摘要 */}
      <div className="mt-4 pt-4 border-t border-border">
        <div className="grid grid-cols-3 gap-4">
          <div className="text-center">
            <div className="text-[12px] text-muted mb-1">总反馈数</div>
            <div className="text-[15px] font-semibold text-foreground">{totalResponses}</div>
          </div>
          <div className="text-center">
            <div className="text-[12px] text-muted mb-1">最高评分</div>
            <div className="text-[15px] font-semibold text-pink-600">
              {Math.max(...data.map(d => d.rating)).toFixed(1)}
            </div>
          </div>
          <div className="text-center">
            <div className="text-[12px] text-muted mb-1">最低评分</div>
            <div className="text-[15px] font-semibold text-orange-600">
              {Math.min(...data.map(d => d.rating)).toFixed(1)}
            </div>
          </div>
        </div>
      </div>
    </DashboardChartCard>
  );
});

UserSatisfactionChart.displayName = 'UserSatisfactionChart';

export default UserSatisfactionChart;

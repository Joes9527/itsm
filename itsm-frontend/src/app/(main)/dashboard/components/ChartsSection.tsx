'use client';

import React from 'react';
import { Card, Row, Col, Spin, Empty } from 'antd';

interface ChartsSectionProps {
  loading?: boolean;
  children?: React.ReactNode;
}

// 图表区域主组件
export const ChartsSection: React.FC<ChartsSectionProps> = React.memo(
  ({ loading = false, children }) => {
    if (loading) {
      return (
        <div className="mb-6">
          <Row gutter={[16, 16]}>
            {Array.from({ length: 4 }).map((_, index) => (
              <Col key={index} xs={24} lg={12}>
                <Card
                  className="rounded-[8px] shadow-none border border-border min-h-[420px]"
                 
                >
                  <div className="flex flex-col items-center justify-center h-full min-h-[400px]">
                    <Spin size="large" />
                    <p className="text-[13px] text-muted mt-4">加载图表数据...</p>
                  </div>
                </Card>
              </Col>
            ))}
          </Row>
        </div>
      );
    }

    if (!children) {
      return (
        <div className="mb-6">
          <Card
            className="rounded-[8px] shadow-none border border-border text-center py-16"
           
          >
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={
                <div>
                  <p className="text-[13px] font-semibold text-foreground mb-1">暂无图表数据</p>
                  <p className="text-[13px] text-muted">系统正在收集和分析数据，请稍后查看</p>
                </div>
              }
            />
          </Card>
        </div>
      );
    }

    return (
      <div className="mb-6">
        <Row gutter={[16, 16]}>{children}</Row>
      </div>
    );
  }
);

ChartsSection.displayName = 'ChartsSection';

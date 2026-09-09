'use client';

import React from 'react';
import { Card, Col, Row } from 'antd';
import styles from './BusinessStatsGrid.module.css';

export type BusinessStatsTone = 'blue' | 'orange' | 'green' | 'purple' | 'cyan' | 'red';

interface BusinessStatsItem {
  label: string;
  value: React.ReactNode;
  icon: React.ReactNode;
  tone: BusinessStatsTone;
}

interface BusinessStatsGridProps {
  items: BusinessStatsItem[];
  className?: string;
  loading?: boolean;
}

const toneClasses: Record<
  BusinessStatsTone,
  {
    iconColor: string;
  }
> = {
  blue: {
    iconColor: '#D85E10',
  },
  orange: {
    iconColor: '#ea580c',
  },
  green: {
    iconColor: '#16a34a',
  },
  purple: {
    iconColor: '#9333ea',
  },
  cyan: {
    iconColor: '#0891b2',
  },
  red: {
    iconColor: '#dc2626',
  },
};

export const BusinessStatsGrid: React.FC<BusinessStatsGridProps> = ({
  items,
  className = '',
  loading = false,
}) => {
  if (loading) {
    return (
      <div className={`mb-6 ${className}`}>
        <Row gutter={[16, 16]} align="stretch">
          {Array.from({ length: 4 }).map((_, index) => (
            <Col key={index} xs={24} sm={12} md={6} lg={6} className="flex">
              <Card loading className="w-full rounded-[8px] shadow-sm" />
            </Col>
          ))}
        </Row>
      </div>
    );
  }

  return (
    <div className={`mb-6 ${className}`}>
      <Row gutter={[16, 16]} align="stretch">
        {items.map((item, index) => {
          const tone = toneClasses[item.tone];
          const toneStyle = {
            '--business-stat-bg': 'var(--color-bg-primary)',
            '--business-stat-border': 'var(--color-border)',
            '--business-stat-icon-bg': 'var(--color-bg-tertiary)',
            '--business-stat-icon-color': tone.iconColor,
            '--business-stat-value-color': 'var(--color-text-primary)',
          } as React.CSSProperties;

          return (
            <Col key={`${item.label}-${index}`} xs={24} sm={12} md={6} lg={6} className="flex">
              <Card
                className={`w-full text-center shadow-sm hover:shadow-lg transition-all duration-300 ${styles.card}`}
                style={toneStyle}
                styles={{ body: { padding: '16px' } }}
              >
                <div
                  className={`mx-auto mb-3 h-10 w-10 rounded-[8px] flex items-center justify-center ${styles.icon}`}
                >
                  {item.icon}
                </div>
                <div className={`${styles.value} text-[26px] font-semibold mb-1`}>{item.value}</div>
                <div className={`${styles.label} font-medium text-[12px]`}>{item.label}</div>
              </Card>
            </Col>
          );
        })}
      </Row>
    </div>
  );
};

export default BusinessStatsGrid;

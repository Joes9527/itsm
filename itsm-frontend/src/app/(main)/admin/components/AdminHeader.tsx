'use client';

import React, { useState, useEffect } from 'react';
import { Card, Col, Row, Typography, theme } from 'antd';
import { useI18n } from '@/lib/i18n';

const { Title, Text } = Typography;

export const AdminHeader: React.FC = () => {
  const { token } = theme.useToken();
  const [currentTime, setCurrentTime] = useState(new Date());
  const { t } = useI18n();

  useEffect(() => {
    const timer = setInterval(() => {
      setCurrentTime(new Date());
    }, 1000);
    return () => clearInterval(timer);
  }, []);

  return (
    <Card
      style={{
        background: token.colorBgContainer,
        marginBottom: token.marginLG,
        border: 'none',
      }}
      styles={{ body: { padding: 16 } }}
    >
      <Row justify="space-between" align="middle">
        <Col>
          <Title
            level={1}
            style={{
              color: token.colorText,
              margin: 0,
              fontSize: 24,
              fontWeight: 600,
              marginBottom: token.marginSM,
            }}
          >
            {t('admin.title')}
          </Title>
          <Text
            style={{
              color: token.colorTextSecondary,
              fontSize: token.fontSize,
            }}
          >
            {t('admin.welcome')}
          </Text>
          <br />
          <Text
            style={{
              color: token.colorTextSecondary,
              fontSize: token.fontSizeSM,
              marginTop: token.marginXS,
            }}
          >
            {t('admin.description')}
          </Text>
        </Col>
        <Col style={{ textAlign: 'right' }}>
          <div
            style={{
              color: token.colorText,
              fontSize: 24,
              fontFamily: 'monospace',
              marginBottom: 4,
            }}
          >
            {currentTime.toLocaleTimeString()}
          </div>
          <Text
            style={{
              color: token.colorTextSecondary,
              fontSize: token.fontSizeSM,
            }}
          >
            {currentTime.toLocaleDateString('zh-CN', {
              year: 'numeric',
              month: 'long',
              day: 'numeric',
              weekday: 'long',
            })}
          </Text>
        </Col>
      </Row>
    </Card>
  );
};

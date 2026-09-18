/**
 * AISuggestionPanel - AI-powered ticket classification suggestions
 *
 * Shows AI-suggested category, priority, and assignee for a ticket.
 * Allows user to accept or dismiss suggestions.
 */

'use client';

import React, { useState, useEffect } from 'react';
import { Card, Typography, Tag, Button, Space, Spin, Progress, Alert } from 'antd';
import { Sparkles, AlertCircle } from 'lucide-react';
import {
  CheckOutlined,
  CloseOutlined,
  SyncOutlined,
} from '@ant-design/icons';
import { aiTriage, type TriageResult } from '@/lib/api/ai-api';

import { useAuthStore } from '@/lib/store/auth-store';

const { Text, Paragraph } = Typography;

interface AISuggestionPanelProps {
  title: string;
  description?: string;
  onAccept?: (suggestion: TriageResult) => void;
  onDismiss?: () => void;
  initialSuggestion?: TriageResult | null;
}

const categoryColors: Record<string, string> = {
  database: 'blue',
  network: 'cyan',
  server: 'purple',
  application: 'green',
  security: 'red',
  storage: 'orange',
  userAccess: 'gold',
  general: 'default',
};

const priorityColors: Record<string, string> = {
  critical: 'red',
  high: 'orange',
  medium: 'blue',
  low: 'green',
};

const categoryLabels: Record<string, string> = {
  database: '数据库',
  network: '网络',
  server: '服务器',
  application: '应用',
  security: '安全',
  storage: '存储',
  userAccess: '用户访问',
  general: '通用',
};

const priorityLabels: Record<string, string> = {
  critical: '紧急',
  high: '高',
  medium: '中',
  low: '低',
};

export function AISuggestionPanel({
  title,
  description,
  onAccept,
  onDismiss,
  initialSuggestion,
}: AISuggestionPanelProps) {
  const hasPermission = useAuthStore(state => state.hasPermission);
  const canUseAI =
    hasPermission('ai:use') || hasPermission('ai:read') || hasPermission('ticket:write');

  const [loading, setLoading] = useState(false);
  const [suggestion, setSuggestion] = useState<TriageResult | null>(initialSuggestion ?? null);
  const [error, setError] = useState<string | null>(null);
  const [collapsed, setCollapsed] = useState(false);
  const [dismissed, setDismissed] = useState(false);

  // Fetch AI suggestion when title/description change
  useEffect(() => {
    if (!title || dismissed || !canUseAI) return;

    const fetchSuggestion = async () => {
      setLoading(true);
      setError(null);
      try {
        const result = await aiTriage(title, description || '');
        setSuggestion(result);
      } catch (err) {
        // AI suggestions are decision support; fail gracefully without crashing
        setError(null);
        setSuggestion(null);
      } finally {
        setLoading(false);
      }
    };

    // Debounce the API call
    const timer = setTimeout(fetchSuggestion, 800);
    return () => clearTimeout(timer);
  }, [title, description, dismissed, canUseAI]);

  const handleAccept = () => {
    if (suggestion && onAccept) {
      onAccept(suggestion);
    }
  };

  const handleDismiss = () => {
    setDismissed(true);
    setSuggestion(null);
    onDismiss?.();
  };

  const handleRefresh = () => {
    setDismissed(false);
    setSuggestion(null);
    setError(null);
  };

  // Don't render if dismissed
  if (dismissed) {
    return (
      <Card
        size="small"
        className="border-dashed border-2 border-border bg-raised"
        styles={{ body: { padding: '12px' } }}
      >
        <div className="flex items-center justify-between">
          <Space>
            <Sparkles className="w-4 h-4 text-muted" />
            <Text type="secondary" className="text-[13px]">
              AI建议已忽略
            </Text>
          </Space>
          <Button
            type="link"
            size="small"
            icon={<SyncOutlined aria-hidden="true" />}
            onClick={handleRefresh}
          >
            重新分析
          </Button>
        </div>
      </Card>
    );
  }

  // Loading state
  if (loading) {
    return (
      <Card
        size="small"
        className="bg-gradient-to-br from-orange-50/60 to-amber-50/40 border-orange-200/70"
        styles={{ body: { padding: '12px' } }}
      >
        <div className="flex items-center gap-3">
          <Spin size="small" />
          <div>
            <Text className="text-[13px] font-medium text-orange-900">AI智能分析中...</Text>
            <Paragraph type="secondary" className="text-[12px] mb-0">
              基于标题和描述分析工单分类
            </Paragraph>
          </div>
        </div>
      </Card>
    );
  }

  // Error state
  if (error) {
    return (
      <Card
        size="small"
        className="bg-raised border-border"
        styles={{ body: { padding: '12px' } }}
      >
        <div className="flex items-center justify-between">
          <Space>
            <AlertCircle className="w-4 h-4 text-muted" />
            <Text type="secondary" className="text-[13px]">
              AI分析暂不可用
            </Text>
          </Space>
          <Button
            type="link"
            size="small"
            icon={<SyncOutlined aria-hidden="true" />}
            onClick={handleRefresh}
          >
            重试
          </Button>
        </div>
      </Card>
    );
  }

  // No suggestion yet
  if (!suggestion) {
    return null;
  }

  const confidencePercent = Math.round(suggestion.confidence * 100);
  const confidenceStatus =
    confidencePercent >= 70 ? 'success' : confidencePercent >= 40 ? 'normal' : 'exception';

  return (
    <Card
      size="small"
      className="bg-gradient-to-br from-orange-50/60 to-amber-50/40 border-orange-200/70"
      styles={{ body: { padding: '12px' } }}
      title={
        <Space>
          <Sparkles className="w-4 h-4 text-orange-500" />
          <span className="font-medium text-orange-900">AI 处置建议</span>
        </Space>
      }
      extra={
        <Button aria-label="关闭"
          type="text"
          size="small"
          icon={<CloseOutlined aria-hidden="true" />}
          onClick={() => setCollapsed(!collapsed)}
        />
      }
    >
      {collapsed ? (
        <Text type="secondary" className="text-[12px]">
          点击展开查看AI分析详情
        </Text>
      ) : (
        <>
          <div className="grid grid-cols-3 gap-3 mb-3">
            {/* Category */}
            <div className="text-center">
              <Text type="secondary" className="text-[12px] block mb-1">
                建议分类
              </Text>
              <Tag color={categoryColors[suggestion.category] || 'default'} className="text-[13px]">
                {categoryLabels[suggestion.category] || suggestion.category}
              </Tag>
            </div>

            {/* Priority */}
            <div className="text-center">
              <Text type="secondary" className="text-[12px] block mb-1">
                建议优先级
              </Text>
              <Tag color={priorityColors[suggestion.priority] || 'default'} className="text-[13px]">
                {priorityLabels[suggestion.priority] || suggestion.priority}
              </Tag>
            </div>

            {/* Confidence */}
            <div className="text-center">
              <Text type="secondary" className="text-[12px] block mb-1">
                置信度
              </Text>
              <Progress
                percent={confidencePercent}
                size="small"
                status={confidenceStatus as 'success' | 'normal' | 'exception'}
                strokeColor={
                  confidencePercent >= 70
                    ? '#52c41a'
                    : confidencePercent >= 40
                      ? '#1890ff'
                      : '#ff4d4f'
                }
                className="mb-0"
              />
            </div>
          </div>

          {/* Explanation */}
          {suggestion.explanation && (
            <Paragraph
              type="secondary"
              className="text-[12px] mb-3 bg-surface rounded p-2"
            >
              {suggestion.explanation}
            </Paragraph>
          )}

          {/* Action buttons */}
          <div className="flex justify-end gap-2">
            <Button size="small" icon={<CloseOutlined aria-hidden="true" />} onClick={handleDismiss}>
              忽略
            </Button>
            <Button
              type="primary"
              size="small"
              icon={<CheckOutlined aria-hidden="true" />}
              onClick={handleAccept}
            >
              采纳建议
            </Button>
          </div>
        </>
      )}
    </Card>
  );
}

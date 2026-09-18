'use client';
import { useDetailResource, useDetailIdentity } from './detail-tabs/useDetailResource';
import { useDetailRefreshEntry } from './detail-tabs/DetailRefreshContext';
import { DetailReadState } from './detail-tabs/DetailReadState';

import React, { useState, useEffect, useRef } from 'react';
import {
  Card,
  List,
  Button,
  Space,
  Typography,
  Tag,
  Empty,
  Badge,
  Modal,
  Form,
  Select,
  Input,
  message,
  Tooltip,
  Divider,
} from 'antd';
import {
  Bell,
  Mail,
  MessageSquare,
  Smartphone,
  Send,
  Eye,
  CheckCircle,
  Clock,
  AlertCircle,
} from 'lucide-react';
import {
  EyeOutlined,
  SendOutlined,
} from '@ant-design/icons';
import type {
  SendTicketNotificationRequest,
} from '@/lib/api/ticket-notification-api';
import { TicketNotificationApi } from '@/lib/api/ticket-notification-api';
import { UserSelect } from '@/components/common/UserSelect';
import { App } from 'antd';
import { useI18n } from '@/lib/i18n';

const { Text, Title } = Typography;
const { TextArea } = Input;

interface TicketNotificationSectionProps {
  ticketId: number;
  canSend?: boolean;
}

/**
 * 工单通知管理组件
 */
export const TicketNotificationSection: React.FC<TicketNotificationSectionProps> = props => {
  const identity = useDetailIdentity(props.ticketId);
  return <TicketNotificationContent key={identity} {...props} />;
};
const TicketNotificationContent: React.FC<TicketNotificationSectionProps> = ({
  ticketId,
  canSend = true,
}) => {
  const { message: antMessage } = App.useApp();
  const messages = useRef(antMessage);
  messages.current = antMessage;
  const { t } = useI18n();
  const busy = useRef(false);
  const [writing, setWriting] = useState(false);
  const resource = useDetailResource(ticketId, async () => {
    const response = await TicketNotificationApi.getTicketNotifications(ticketId);
    return response.notifications || [];
  }, rows => rows.length);
  const notifications = resource.data || [];
  useDetailRefreshEntry({ key: 'notifications', label: '通知', reload: resource.reload, isWriting: () => busy.current });
  const [sendModalVisible, setSendModalVisible] = useState(false);
  const [eventTypes, setEventTypes] = useState<Array<{ code: string; name: string }>>([]);
  const [form] = Form.useForm();

  useEffect(() => {
    if (!resource.denied) return;
    busy.current = false;
    setWriting(false);
    setSendModalVisible(false);
    setEventTypes([]);
    form.resetFields();
  }, [resource.denied, form]);
  useEffect(() => {
    if (!canSend) { setSendModalVisible(false); form.resetFields(); }
  }, [canSend, form]);
  useEffect(() => {
    if (!sendModalVisible) return;
    const current = resource.capture();
    let active = true;
    TicketNotificationApi.getNotificationPreferences()
      .then(response => { if (active && current()) setEventTypes(response.eventTypes || []); })
      .catch(() => { if (active && current()) messages.current.error('加载通知事件类型失败'); });
    return () => { active = false; };
  }, [sendModalVisible, resource.capture]);

  // 发送通知
  const handleSendNotification = async (values: {
    userIds: number[];
    eventType: string;
    content: string;
  }) => {
    if (!canSend || !resource.ready || busy.current) return;
    const current = resource.capture();
    busy.current = true;
    setWriting(true);
    try {
      const request: SendTicketNotificationRequest = {
        userIds: values.userIds,
        eventType: values.eventType,
        content: values.content,
      };
      const result = await TicketNotificationApi.sendTicketNotification(ticketId, request);
      if (!current()) return;
      antMessage.success(
        result.effect === 'queued'
          ? '已加入发送队列'
          : result.effect === 'idempotent'
            ? '该通知已受理'
            : `已生成 ${result.appliedCount} 条站内通知`
      );
      setSendModalVisible(false);
      form.resetFields();
      await resource.reload({ afterWrite: true });
    } catch (error: unknown) {
      if (!current()) return;
      resource.deny(error);
      antMessage.error(error instanceof Error ? error.message : '通知发送失败');
    } finally {
      if (current()) { busy.current = false; setWriting(false); }
    }
  };

  // 标记为已读
  const handleMarkRead = async (notificationId: number) => {
    if (!resource.ready || busy.current) return;
    const current = resource.capture();
    busy.current = true;
    setWriting(true);
    try {
      await TicketNotificationApi.markTicketNotificationRead(notificationId);
      if (!current()) return;
      antMessage.success('已标记为已读');
      await resource.reload({ afterWrite: true });
    } catch (error: unknown) {
      if (!current()) return;
      resource.deny(error);
      antMessage.error(error instanceof Error ? error.message : '标记失败');
    } finally {
      if (current()) { busy.current = false; setWriting(false); }
    }
  };

  // 获取通知类型标签
  const getNotificationTypeLabel = (type: string) => {
    const labels: Record<string, string> = {
      created: '工单创建',
      assigned: '工单分配',
      statusChanged: '状态变更',
      commented: '新增评论',
      slaWarning: 'SLA警告',
      resolved: '工单已解决',
      closed: '工单已关闭',
    };
    return labels[type] || type;
  };

  // 获取通知类型颜色
  const getNotificationTypeColor = (type: string) => {
    const colors: Record<string, string> = {
      created: 'blue',
      assigned: 'cyan',
      statusChanged: 'orange',
      commented: 'purple',
      slaWarning: 'red',
      resolved: 'green',
      closed: 'default',
    };
    return colors[type] || 'default';
  };

  // 获取渠道图标
  const getChannelIcon = (channel: string) => {
    switch (channel) {
      case 'email':
        return <Mail style={{ fontSize: 16 }} />;
      case 'sms':
        return <Smartphone style={{ fontSize: 16 }} />;
      default:
        return <MessageSquare style={{ fontSize: 16 }} />;
    }
  };

  // 获取渠道颜色
  const getChannelColor = (channel: string) => {
    switch (channel) {
      case 'email':
        return 'blue';
      case 'sms':
        return 'green';
      default:
        return 'default';
    }
  };

  // 格式化时间
  const formatDateTime = (dateString?: string) => {
    if (!dateString) return '';
    return new Date(dateString).toLocaleString('zh-CN');
  };

  // 未读通知数量
  const unreadCount = notifications.filter(notification => !notification.readAt).length;

  return (
    <div className="space-y-4">
      <DetailReadState error={resource.error} loading={resource.loading} reload={resource.reload} />
      {/* 操作栏 */}
      <div className="flex justify-between items-center">
        <Space>
          <Title level={5} style={{ margin: 0 }}>
            通知历史
          </Title>
          {unreadCount > 0 && (
            <Badge count={unreadCount} showZero>
              <Bell style={{ fontSize: 20 }} />
            </Badge>
          )}
        </Space>
        {canSend && !resource.denied && (
          <Button type="primary" icon={<SendOutlined aria-hidden="true" />} disabled={!resource.ready || writing} onClick={() => setSendModalVisible(true)}>
            发送通知
          </Button>
        )}
      </div>

      {/* 通知列表 */}
      <Card loading={resource.loading && !resource.ready}>
        {notifications.length === 0 ? (
          resource.ready ? <Empty description="暂无通知" /> : null
        ) : (
          <List
            dataSource={notifications}
            renderItem={notification => {
              const isRead = Boolean(notification.readAt);
              return (
                <List.Item
                  className={isRead ? 'opacity-70' : ''}
                  actions={[
                    !isRead && (
                      <Tooltip title="标记为已读" key="read">
                        <Button
                          type="text"
                          size="small"
                          icon={<EyeOutlined aria-hidden="true" />}
                          disabled={writing} onClick={() => handleMarkRead(notification.id)}
                        >
                          标记已读
                        </Button>
                      </Tooltip>
                    ),
                  ].filter(Boolean)}
                >
                  <List.Item.Meta
                    avatar={
                      <div className="flex items-center justify-center w-10 h-10 rounded-full bg-blue-50">
                        {isRead ? (
                          <CheckCircle style={{ fontSize: 20, color: '#52c41a' }} />
                        ) : (
                          <Bell style={{ fontSize: 20, color: '#1890ff' }} />
                        )}
                      </div>
                    }
                    title={
                      <Space>
                        <Tag color={getNotificationTypeColor(notification.type)}>
                          {getNotificationTypeLabel(notification.type)}
                        </Tag>
                        <Tag
                          color={getChannelColor(notification.channel)}
                          icon={getChannelIcon(notification.channel)}
                        >
                          {notification.channel === 'email'
                            ? '邮件'
                            : notification.channel === 'sms'
                              ? '短信'
                              : '站内消息'}
                        </Tag>
                        {!isRead && <Badge status="processing" text="未读" />}
                      </Space>
                    }
                    description={
                      <div className="space-y-1">
                        <Text>{notification.content}</Text>
                        <div className="flex items-center space-x-4 text-[12px] text-muted">
                          <span>
                            <Clock style={{ fontSize: 12, marginRight: 4 }} />
                            创建时间: {formatDateTime(notification.createdAt)}
                          </span>
                          {notification.sentAt && (
                            <span>
                              <Send style={{ fontSize: 12, marginRight: 4 }} />
                              发送时间: {formatDateTime(notification.sentAt)}
                            </span>
                          )}
                          {notification.readAt && (
                            <span>
                              <Eye style={{ fontSize: 12, marginRight: 4 }} />
                              阅读时间: {formatDateTime(notification.readAt)}
                            </span>
                          )}
                          {notification.user && (
                            <span>
                              接收人: {notification.user.name || notification.user.username}
                            </span>
                          )}
                        </div>
                      </div>
                    }
                  />
                </List.Item>
              );
            }}
          />
        )}
      </Card>

      {/* 发送通知模态框 */}
      <Modal
        title="发送通知"
        open={sendModalVisible}
        confirmLoading={writing}
        onOk={() => form.submit()}
        onCancel={() => {
          setSendModalVisible(false);
          form.resetFields();
        }}
        okText={t('common.submit') || '发送'}
        cancelText={t('common.cancel')}
        width={600}
      >
        <Form form={form} layout="vertical" onFinish={handleSendNotification}>
          <Form.Item
            label="接收人"
            name="userIds"
            rules={[{ required: true, message: '请选择接收人' }]}
          >
            <UserSelect mode="multiple" placeholder="请选择接收人" />
          </Form.Item>

          <Form.Item
            label="通知类型"
            name="eventType"
            rules={[{ required: true, message: '请选择通知类型' }]}
          >
            <Select
              placeholder="请选择通知类型"
              options={eventTypes.map(eventType => ({
                value: eventType.code,
                label: eventType.name,
              }))}
            />
          </Form.Item>

          <Form.Item
            label="通知内容"
            name="content"
            rules={[{ required: true, message: '请输入通知内容' }]}
          >
            <TextArea rows={4} placeholder="请输入通知内容..." showCount maxLength={500} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

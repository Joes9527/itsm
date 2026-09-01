'use client';

import React, { useState, useEffect } from 'react';
import {
  Card,
  Form,
  Input,
  Button,
  Avatar,
  Typography,
  Space,
  Divider,
  message,
  Row,
  Col,
  Tabs,
  Upload,
  Select,
  Switch,
  Statistic,
  Progress,
  Tag,
  Table,
  Modal,
  Tooltip,
} from 'antd';
import {
  User,
  Mail,
  Phone,
  Building,
  Shield,
  Bell,
  Key,
  Camera,
  Save,
  Edit,
  Ticket,
  Clock,
  CheckCircle,
  Star,
  Settings,
  Lock,
  LogOut,
  Activity,
} from 'lucide-react';
import { PageHeader } from '@/components/layout/PageHeader';
import { UserApi } from '@/lib/api/user-api';
import { TicketApi } from '@/lib/api/ticket-api';
import { NotificationPreferenceApi, NotificationEventType } from '@/lib/api/notification-preference-api';
import { useI18n } from '@/lib/i18n';
import { useAuthStore } from '@/lib/store/auth-store';

const { Title, Text } = Typography;

// 独特的设计系统
const DESIGN = {
  colors: {
    primary: '#0f172a',
    accent: '#F06820',
    success: '#10b981',
    warning: '#f59e0b',
    danger: '#ef4444',
    surface: '#ffffff',
    border: '#e2e8f0',
    text: '#1e293b',
    textMuted: '#64748b',
    bgSubtle: '#f8fafc',
    gradient: {
      primary: 'linear-gradient(135deg, #F06820 0%, #B84A08 100%)',
      success: 'linear-gradient(135deg, #10b981 0%, #059669 100%)',
      warning: 'linear-gradient(135deg, #f59e0b 0%, #d97706 100%)',
    },
  },
  shadows: {
    card: '0 1px 3px rgb(0 0 0 / 0.05)',
    cardHover: '0 10px 40px -10px rgb(0 0 0 / 0.15)',
    glow: (color: string) => `0 0 30px ${color}20`,
  },
  radius: {
    sm: '8px',
    md: '12px',
    lg: '16px',
    xl: '20px',
    full: '9999px',
  },
};

interface UserProfile {
  id: number;
  username: string;
  email: string;
  name: string;
  department?: string;
  phone?: string;
  role?: string;
  avatar?: string;
  tenantId?: number;
  createdAt?: string;
  lastLogin?: string;
}

interface UserStats {
  totalTickets: number;
  resolvedTickets: number;
  avgResolutionTime: number;
  satisfactionScore: number;
  responseRate: number;
}

interface ActivityItem {
  id: number;
  action: string;
  target: string;
  time: string;
  status: 'completed' | 'pending' | 'cancelled';
}

// 后端不可用时的通知事件类型 fallback（与后端 dto.ListNotificationEventTypes 对齐）
const DEFAULT_NOTIFICATION_EVENT_TYPES: NotificationEventType[] = [
  { code: 'ticket_created', name: '工单创建', description: '当工单被创建时' },
  { code: 'ticket_assigned', name: '工单分配', description: '当工单被分配给自己时' },
  { code: 'ticket_updated', name: '工单更新', description: '当工单被更新时' },
  { code: 'ticket_resolved', name: '工单解决', description: '当工单被解决时' },
  { code: 'ticket_closed', name: '工单关闭', description: '当工单被关闭时' },
  { code: 'sla_warning', name: 'SLA警告', description: '当SLA即将超时时' },
  { code: 'sla_violated', name: 'SLA违规', description: '当SLA超时时' },
  { code: 'comment_added', name: '新增评论', description: '当工单新增评论时' },
  { code: 'approval_required', name: '需要审批', description: '当需要审批时' },
  { code: 'mention', name: '被提及', description: '当被@提及 时' },
];

export default function ProfilePage() {
  const { t } = useI18n();
  const { user } = useAuthStore();
  const [profile, setProfile] = useState<UserProfile | null>(null);
  const [editing, setEditing] = useState(false);
  const [loading, setLoading] = useState(false);
  const [stats, setStats] = useState<UserStats | null>(null);
  const [activities, setActivities] = useState<ActivityItem[]>([]);
  const [profileForm] = Form.useForm();
  const [preferencesForm] = Form.useForm();

  const [passwordModalVisible, setPasswordModalVisible] = useState(false);
  const [passwordForm] = Form.useForm();
  const [passwordLoading, setPasswordLoading] = useState(false);
  const [prefsLoading, setPrefsLoading] = useState(false);
  const [eventTypes, setEventTypes] = useState<NotificationEventType[]>([]);
  const [prefMatrix, setPrefMatrix] = useState<
    Record<string, { email: boolean; sms: boolean; inApp: boolean; push: boolean }>
  >({});

  const handlePasswordChange = async () => {
    try {
      const values = await passwordForm.validateFields();

      if (values.newPassword !== values.confirmPassword) {
        message.error('两次输入的密码不一致');
        return;
      }

      if (!profile?.id) {
        message.error('用户信息不完整');
        return;
      }

      setPasswordLoading(true);
      await UserApi.resetPassword(profile.id, values.newPassword);
      message.success('密码修改成功');
      setPasswordModalVisible(false);
      passwordForm.resetFields();
    } catch (error) {
      console.error('Failed to change password:', error);
      message.error('密码修改失败，请重试');
    } finally {
      setPasswordLoading(false);
    }
  };

  useEffect(() => {
    loadProfile();
    loadStats();
    loadActivities();
    loadNotificationPreferences();
  }, []);

  const loadProfile = async () => {
    try {
      setLoading(true);
      if (user) {
        const userData = {
          id: user.id,
          username: user.username || '',
          email: user.email || '',
          name: user.name || '',
          department: user.department || '',
          phone: '',
          role: String(user.role),
          createdAt: user.createdAt || new Date().toISOString(),
          lastLogin: undefined,
        };
        setProfile(userData);
        profileForm.setFieldsValue(userData);
      }
    } catch (error) {
      console.error('Failed to load profile:', error);
    } finally {
      setLoading(false);
    }
  };

  const loadStats = async () => {
    try {
      const ticketStats = await TicketApi.getTicketStats();
      setStats({
        totalTickets: ticketStats.total || 0,
        resolvedTickets: ticketStats.resolved || 0,
        avgResolutionTime: 0, // 需要后端支持
        satisfactionScore: 0, // 需要后端支持
        responseRate:
          ticketStats.total > 0
            ? Math.round(((ticketStats.total - ticketStats.open) / ticketStats.total) * 100)
            : 0,
      });
    } catch (error) {
      console.error('Failed to load stats:', error);
      // 使用默认值
      setStats({
        totalTickets: 0,
        resolvedTickets: 0,
        avgResolutionTime: 0,
        satisfactionScore: 0,
        responseRate: 0,
      });
    }
  };

  const loadActivities = async () => {
    try {
      // 从 localStorage 获取用户活动记录
      const savedActivities = localStorage.getItem('user_activities');
      if (savedActivities) {
        setActivities(JSON.parse(savedActivities));
      } else {
        // 如果没有记录，使用空数组
        setActivities([]);
      }
    } catch (error) {
      console.error('Failed to load activities:', error);
      setActivities([]);
    }
  };

  const handleSaveProfile = async (values: any) => {
    if (!profile?.id) {
      message.error('用户信息不完整');
      return;
    }
    setLoading(true);
    try {
      await UserApi.updateUser(profile.id, {
        name: values.name,
        email: values.email,
        phone: values.phone,
        department: values.department,
      });
      message.success('个人信息更新成功');
      setEditing(false);
      setProfile(prev => (prev ? { ...prev, ...values } : null));

      // 更新 auth store 中的用户信息
      const { updateUser } = useAuthStore.getState();
      updateUser({ ...values });
    } catch (error) {
      console.error('Failed to update profile:', error);
      message.error('更新失败，请重试');
    } finally {
      setLoading(false);
    }
  };

  const handleSavePreferences = async () => {
    try {
      setPrefsLoading(true);
      const timezone = preferencesForm.getFieldValue('timezone') || 'Asia/Shanghai';
      // 基于矩阵状态构建批量更新请求（按事件类型 × 4 渠道）
      const preferences = eventTypes.map(et => {
        const row = prefMatrix[et.code] || { email: true, sms: false, inApp: true, push: false };
        return {
          eventType: et.code,
          emailEnabled: row.email,
          smsEnabled: row.sms,
          inAppEnabled: row.inApp,
          pushEnabled: row.push,
          timezone,
          frequency: 'immediate',
        };
      });

      await NotificationPreferenceApi.bulkUpdate({ preferences });
      message.success('偏好设置已保存到服务器');
    } catch (error) {
      console.error('Failed to save preferences:', error);
      message.error('保存失败，请重试');
    } finally {
      setPrefsLoading(false);
    }
  };

  // 获取用户首字母
  const userInitial = (profile?.name || profile?.username || 'U').charAt(0).toUpperCase();

  // 加载通知偏好设置
  const loadNotificationPreferences = async () => {
    try {
      const data = await NotificationPreferenceApi.getPreferences();
      // 事件类型列表（后端返回 [{code,name,description}]）
      const types: NotificationEventType[] =
        data.eventTypes && data.eventTypes.length > 0
          ? data.eventTypes
          : DEFAULT_NOTIFICATION_EVENT_TYPES;
      setEventTypes(types);

      // 构建矩阵：默认 email+in_app 启用，sms+push 禁用
      const matrix: Record<string, { email: boolean; sms: boolean; inApp: boolean; push: boolean }> = {};
      types.forEach(et => {
        matrix[et.code] = { email: true, sms: false, inApp: true, push: false };
      });
      (data.preferences || []).forEach(p => {
        matrix[p.eventType] = {
          email: p.emailEnabled,
          sms: p.smsEnabled,
          inApp: p.inAppEnabled,
          push: p.pushEnabled,
        };
      });
      setPrefMatrix(matrix);

      const timezone = (data.preferences || []).find(p => p.timezone)?.timezone || 'Asia/Shanghai';
      preferencesForm.setFieldsValue({ language: 'zh-CN', timezone });
    } catch (error) {
      console.error('Failed to load notification preferences:', error);
      preferencesForm.setFieldsValue({ language: 'zh-CN', timezone: 'Asia/Shanghai' });
    }
  };

  // 更新矩阵中某个事件类型的某个渠道开关
  const updatePref = (
    eventType: string,
    channel: 'email' | 'sms' | 'inApp' | 'push',
    checked: boolean
  ) => {
    setPrefMatrix(prev => ({
      ...prev,
      [eventType]: {
        ...(prev[eventType] || { email: true, sms: false, inApp: true, push: false }),
        [channel]: checked,
      },
    }));
  };

  // 角色标签颜色
  const roleColor =
    profile?.role === 'admin' || profile?.role === 'super_admin'
      ? DESIGN.colors.accent
      : DESIGN.colors.textMuted;

  return (
    <div className="min-h-screen" style={{ background: DESIGN.colors.bgSubtle }}>
      <PageHeader title="个人中心" />

      <div style={{ padding: '24px', maxWidth: 1200, margin: '0 auto' }}>
        <Row gutter={[24, 24]}>
          {/* 左侧 - 用户信息卡片 */}
          <Col xs={24} lg={8}>
            <Card
              style={{
                borderRadius: DESIGN.radius.lg,
                border: 'none',
                boxShadow: DESIGN.shadows.card,
                overflow: 'hidden',
              }}
              styles={{ body: { padding: 0 } }}
            >
              {/* 头部背景 */}
              <div
                style={{
                  height: 120,
                  background: DESIGN.colors.gradient.primary,
                  position: 'relative',
                }}
              >
                {/* 编辑按钮 */}
                <Button
                  type="text"
                  icon={<Settings size={18} />}
                  onClick={() => setEditing(!editing)}
                  style={{
                    position: 'absolute',
                    top: 16,
                    right: 16,
                    color: 'rgba(255,255,255,0.8)',
                    background: 'rgba(255,255,255,0.2)',
                  }}
                />
              </div>

              {/* 头像和信息 */}
              <div style={{ padding: '0 24px 24px', marginTop: -50 }}>
                <div style={{ display: 'flex', justifyContent: 'center', marginBottom: 16 }}>
                  <Avatar
                    size={100}
                    style={{
                      background: DESIGN.colors.gradient.primary,
                      fontSize: 40,
                      fontWeight: 700,
                      border: '4px solid white',
                      boxShadow: DESIGN.shadows.cardHover,
                    }}
                  >
                    {userInitial}
                  </Avatar>
                </div>

                <div style={{ textAlign: 'center', marginBottom: 16 }}>
                  <Title level={4} style={{ margin: 0, marginBottom: 4 }}>
                    {profile?.name || profile?.username || '用户'}
                  </Title>
                  <Text style={{ color: DESIGN.colors.textMuted }}>
                    {profile?.email || 'user@example.com'}
                  </Text>
                </div>

                <div
                  style={{ display: 'flex', justifyContent: 'center', gap: 8, marginBottom: 20 }}
                >
                  <Tag
                    color={roleColor}
                    style={{
                      padding: '4px 12px',
                      borderRadius: DESIGN.radius.full,
                      fontWeight: 600,
                    }}
                  >
                    {profile?.role === 'admin'
                      ? '管理员'
                      : profile?.role === 'super_admin'
                        ? '超级管理员'
                        : '用户'}
                  </Tag>
                  <Tag
                    style={{
                      padding: '4px 12px',
                      borderRadius: DESIGN.radius.full,
                      background: `${DESIGN.colors.success}15`,
                      color: DESIGN.colors.success,
                      border: 'none',
                    }}
                  >
                    <CheckCircle size={12} /> 已激活
                  </Tag>
                </div>

                <Divider style={{ margin: '16px 0' }} />

                {/* 用户详情 */}
                <Space orientation="vertical" style={{ width: '100%' }} size="middle">
                  <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                    <Building size={16} style={{ color: DESIGN.colors.textMuted }} />
                    <Text>{profile?.department || '未设置'}</Text>
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                    <Phone size={16} style={{ color: DESIGN.colors.textMuted }} />
                    <Text>{profile?.phone || '未设置'}</Text>
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                    <Clock size={16} style={{ color: DESIGN.colors.textMuted }} />
                    <Text>上次登录: {profile?.lastLogin || '刚刚'}</Text>
                  </div>
                </Space>
              </div>
            </Card>

            {/* 统计卡片 */}
            <Card
              style={{
                marginTop: 24,
                borderRadius: DESIGN.radius.lg,
                border: 'none',
                boxShadow: DESIGN.shadows.card,
              }}
            >
              <Title level={5} style={{ marginBottom: 20 }}>
                工作统计
              </Title>
              <Row gutter={[16, 16]}>
                <Col span={12}>
                  <Statistic
                    title="提交工单"
                    value={stats?.totalTickets || 0}
                    prefix={<Ticket size={16} style={{ color: DESIGN.colors.accent }} />}
                  />
                </Col>
                <Col span={12}>
                  <Statistic
                    title="已解决"
                    value={stats?.resolvedTickets || 0}
                    prefix={<CheckCircle size={16} style={{ color: DESIGN.colors.success }} />}
                  />
                </Col>
                <Col span={12}>
                  <Statistic
                    title="满意度"
                    value={stats?.satisfactionScore || 0}
                    suffix="/5"
                    prefix={<Star size={16} style={{ color: DESIGN.colors.warning }} />}
                  />
                </Col>
                <Col span={12}>
                  <Statistic
                    title="响应率"
                    value={stats?.responseRate || 0}
                    suffix="%"
                    prefix={<Activity size={16} style={{ color: DESIGN.colors.accent }} />}
                  />
                </Col>
              </Row>
            </Card>
          </Col>

          {/* 右侧 - 内容区域 */}
          <Col xs={24} lg={16}>
            <Card
              style={{
                borderRadius: DESIGN.radius.lg,
                border: 'none',
                boxShadow: DESIGN.shadows.card,
              }}
            >
              <Tabs
                defaultActiveKey="basic"
                size="large"
                items={[
                  {
                    key: 'basic',
                    label: (
                      <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        <User size={16} />
                        基本信息
                      </span>
                    ),
                    children: (
                      <Form
                        form={profileForm}
                        layout="vertical"
                        onFinish={handleSaveProfile}
                        initialValues={profile || undefined}
                      >
                        <Row gutter={24}>
                          <Col xs={24} md={12}>
                            <Form.Item
                              label="姓名"
                              name="name"
                              rules={[{ required: true, message: '请输入姓名' }]}
                            >
                              <Input
                                data-testid="profile-name-input"
                                aria-label="姓名"
                                prefix={
                                  <User size={16} style={{ color: DESIGN.colors.textMuted }} />
                                }
                              />
                            </Form.Item>
                          </Col>
                          <Col xs={24} md={12}>
                            <Form.Item label="用户名" name="username">
                              <Input
                                disabled
                                prefix={
                                  <User size={16} style={{ color: DESIGN.colors.textMuted }} />
                                }
                              />
                            </Form.Item>
                          </Col>
                          <Col xs={24} md={12}>
                            <Form.Item
                              label="邮箱"
                              name="email"
                              rules={[{ required: true, type: 'email', message: '请输入有效邮箱' }]}
                            >
                              <Input
                                prefix={
                                  <Mail size={16} style={{ color: DESIGN.colors.textMuted }} />
                                }
                              />
                            </Form.Item>
                          </Col>
                          <Col xs={24} md={12}>
                            <Form.Item label="电话" name="phone">
                              <Input
                                prefix={
                                  <Phone size={16} style={{ color: DESIGN.colors.textMuted }} />
                                }
                              />
                            </Form.Item>
                          </Col>
                          <Col xs={24} md={12}>
                            <Form.Item label="部门" name="department">
                              <Input
                                prefix={
                                  <Building size={16} style={{ color: DESIGN.colors.textMuted }} />
                                }
                              />
                            </Form.Item>
                          </Col>
                          <Col xs={24} md={12}>
                            <Form.Item label="角色">
                              <Tag color={roleColor} style={{ padding: '4px 12px' }}>
                                {profile?.role === 'admin'
                                  ? '管理员'
                                  : profile?.role === 'super_admin'
                                    ? '超级管理员'
                                    : '用户'}
                              </Tag>
                            </Form.Item>
                          </Col>
                        </Row>

                        <div style={{ marginTop: 24, textAlign: 'right' }}>
                          <Space>
                            {editing && <Button onClick={() => setEditing(false)}>取消</Button>}
                            <Button
                              type="primary"
                              htmlType="submit"
                              icon={<Save size={16} />}
                              style={{
                                background: DESIGN.colors.gradient.primary,
                                boxShadow: DESIGN.shadows.glow(DESIGN.colors.accent),
                              }}
                            >
                              保存修改
                            </Button>
                          </Space>
                        </div>
                      </Form>
                    ),
                  },
                  {
                    key: 'preferences',
                    label: (
                      <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        <Settings size={16} />
                        偏好设置
                      </span>
                    ),
                    children: (
                      <Form
                        form={preferencesForm}
                        layout="vertical"
                        onFinish={handleSavePreferences}
                        initialValues={{
                          language: 'zh-CN',
                          timezone: 'Asia/Shanghai',
                        }}
                      >
                        <Title level={5}>通知设置</Title>
                        <Row gutter={24}>
                          <Col xs={24}>
                            <Form.Item label="语言" name="language">
                              <Select>
                                <Select.Option value="zh-CN">简体中文</Select.Option>
                                <Select.Option value="en-US">English</Select.Option>
                              </Select>
                            </Form.Item>
                          </Col>
                          <Col xs={24}>
                            <Form.Item label="时区" name="timezone">
                              <Select>
                                <Select.Option value="Asia/Shanghai">
                                  中国标准时间 (UTC+8)
                                </Select.Option>
                                <Select.Option value="UTC">UTC</Select.Option>
                              </Select>
                            </Form.Item>
                          </Col>
                          <Col xs={24}>
                            <Table<NotificationEventType & { key: string }>
                              dataSource={eventTypes.map(et => ({ key: et.code, ...et }))}
                              pagination={false}
                              size="small"
                              columns={[
                                {
                                  title: '事件类型',
                                  key: 'name',
                                  render: (_: unknown, record: NotificationEventType & { key: string }) => (
                                    <div>
                                      <div>{record.name}</div>
                                      <div style={{ fontSize: 12, color: DESIGN.colors.textMuted }}>
                                        {record.description}
                                      </div>
                                    </div>
                                  ),
                                },
                                {
                                  title: '邮件',
                                  key: 'email',
                                  width: 80,
                                  align: 'center',
                                  render: (_: unknown, record: { key: string }) => (
                                    <Switch
                                      size="small"
                                      checked={prefMatrix[record.key]?.email ?? true}
                                      onChange={checked => updatePref(record.key, 'email', checked)}
                                    />
                                  ),
                                },
                                {
                                  title: '站内',
                                  key: 'inApp',
                                  width: 80,
                                  align: 'center',
                                  render: (_: unknown, record: { key: string }) => (
                                    <Switch
                                      size="small"
                                      checked={prefMatrix[record.key]?.inApp ?? true}
                                      onChange={checked => updatePref(record.key, 'inApp', checked)}
                                    />
                                  ),
                                },
                                {
                                  title: '短信',
                                  key: 'sms',
                                  width: 80,
                                  align: 'center',
                                  render: (_: unknown, record: { key: string }) => (
                                    <Switch
                                      size="small"
                                      checked={prefMatrix[record.key]?.sms ?? false}
                                      onChange={checked => updatePref(record.key, 'sms', checked)}
                                    />
                                  ),
                                },
                                {
                                  title: '推送',
                                  key: 'push',
                                  width: 80,
                                  align: 'center',
                                  render: (_: unknown, record: { key: string }) => (
                                    <Switch
                                      size="small"
                                      checked={prefMatrix[record.key]?.push ?? false}
                                      onChange={checked => updatePref(record.key, 'push', checked)}
                                    />
                                  ),
                                },
                              ]}
                            />
                          </Col>
                        </Row>

                        <div style={{ marginTop: 24, textAlign: 'right' }}>
                          <Button
                            type="primary"
                            htmlType="submit"
                            icon={<Save size={16} />}
                            loading={prefsLoading}
                            style={{
                              background: DESIGN.colors.gradient.primary,
                            }}
                          >
                            保存设置
                          </Button>
                        </div>
                      </Form>
                    ),
                  },
                  {
                    key: 'activities',
                    label: (
                      <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        <Activity size={16} />
                        最近活动
                      </span>
                    ),
                    children: (
                      <div>
                        {activities.length === 0 ? (
                          <div
                            style={{
                              textAlign: 'center',
                              padding: '40px 0',
                              color: DESIGN.colors.textMuted,
                            }}
                          >
                            暂无活动记录
                          </div>
                        ) : (
                          activities.map((activity, index) => (
                            <div
                              key={activity.id}
                              style={{
                                display: 'flex',
                                gap: 16,
                                padding: '16px 0',
                                borderBottom:
                                  index < activities.length - 1
                                    ? `1px solid ${DESIGN.colors.border}`
                                    : 'none',
                              }}
                            >
                              <div
                                style={{
                                  width: 40,
                                  height: 40,
                                  borderRadius: DESIGN.radius.md,
                                  background: `${DESIGN.colors.success}15`,
                                  display: 'flex',
                                  alignItems: 'center',
                                  justifyContent: 'center',
                                  color: DESIGN.colors.success,
                                }}
                              >
                                <CheckCircle size={18} />
                              </div>
                              <div style={{ flex: 1 }}>
                                <div style={{ fontWeight: 500, marginBottom: 4 }}>
                                  {activity.action}
                                </div>
                                <div style={{ color: DESIGN.colors.textMuted, fontSize: 13 }}>
                                  {activity.target}
                                </div>
                              </div>
                              <div style={{ color: DESIGN.colors.textMuted, fontSize: 13 }}>
                                {activity.time}
                              </div>
                            </div>
                          ))
                        )}
                      </div>
                    ),
                  },
                  {
                    key: 'security',
                    label: (
                      <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        <Lock size={16} />
                        安全设置
                      </span>
                    ),
                    children: (
                      <>
                        <Card
                          size="small"
                          style={{
                            borderRadius: DESIGN.radius.md,
                            border: `1px solid ${DESIGN.colors.border}`,
                          }}
                        >
                          <div
                            style={{
                              display: 'flex',
                              justifyContent: 'space-between',
                              alignItems: 'center',
                            }}
                          >
                            <div>
                              <div style={{ fontWeight: 600, marginBottom: 4 }}>修改密码</div>
                              <div style={{ color: DESIGN.colors.textMuted, fontSize: 13 }}>
                                定期修改密码可以保护账户安全
                              </div>
                            </div>
                            <Button
                              icon={<Key size={16} />}
                              onClick={() => setPasswordModalVisible(true)}
                            >
                              修改
                            </Button>
                          </div>
                        </Card>

                        <Card
                          size="small"
                          style={{
                            borderRadius: DESIGN.radius.md,
                            border: `1px solid ${DESIGN.colors.border}`,
                          }}
                        >
                          <div
                            style={{
                              display: 'flex',
                              justifyContent: 'space-between',
                              alignItems: 'center',
                            }}
                          >
                            <div>
                              <div style={{ fontWeight: 600, marginBottom: 4 }}>两步验证</div>
                              <div style={{ color: DESIGN.colors.textMuted, fontSize: 13 }}>
                                为账户添加额外的安全保护
                              </div>
                            </div>
                            <Tooltip title="该功能即将推出">
                              <Button type="primary" ghost disabled>
                                启用
                              </Button>
                            </Tooltip>
                          </div>
                        </Card>

                        <Card
                          size="small"
                          style={{
                            borderRadius: DESIGN.radius.md,
                            border: `1px solid ${DESIGN.colors.border}`,
                          }}
                        >
                          <div
                            style={{
                              display: 'flex',
                              justifyContent: 'space-between',
                              alignItems: 'center',
                            }}
                          >
                            <div>
                              <div style={{ fontWeight: 600, marginBottom: 4 }}>登录历史</div>
                              <div style={{ color: DESIGN.colors.textMuted, fontSize: 13 }}>
                                查看账户的登录历史记录
                              </div>
                            </div>
                            <Tooltip title="该功能即将推出">
                              <Button disabled>查看</Button>
                            </Tooltip>
                          </div>
                        </Card>
                      </>
                    ),
                  },
                ]}
              />
            </Card>
          </Col>
        </Row>
      </div>

      {/* 修改密码模态框 */}
      <Modal
        title="修改密码"
        open={passwordModalVisible}
        onOk={handlePasswordChange}
        onCancel={() => {
          setPasswordModalVisible(false);
          passwordForm.resetFields();
        }}
        confirmLoading={passwordLoading}
        okText="确认修改"
        cancelText="取消"
      >
        <Form form={passwordForm} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item
            name="oldPassword"
            label="当前密码"
            rules={[{ required: true, message: '请输入当前密码' }]}
          >
            <Input.Password placeholder="请输入当前密码" />
          </Form.Item>
          <Form.Item
            name="newPassword"
            label="新密码"
            rules={[
              { required: true, message: '请输入新密码' },
              { min: 6, message: '密码长度至少6位' },
            ]}
          >
            <Input.Password placeholder="请输入新密码" />
          </Form.Item>
          <Form.Item
            name="confirmPassword"
            label="确认新密码"
            rules={[{ required: true, message: '请确认新密码' }]}
          >
            <Input.Password placeholder="请再次输入新密码" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}

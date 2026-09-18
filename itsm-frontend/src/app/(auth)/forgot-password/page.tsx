'use client';

import React, { useState } from 'react';
import { useRouter } from 'next/navigation';
import { Mail, CheckCircle } from 'lucide-react';
import {
  ArrowLeftOutlined,
  SafetyOutlined,
} from '@ant-design/icons';
import { useI18n } from '@/lib/i18n/useI18n';
import {
  Typography,
  Form,
  Input,
  Button,
  Card,
  ConfigProvider,
  message,
  Divider,
  Alert,
} from 'antd';
import { AuthService } from '@/lib/services/auth-service';
import { logger } from '@/lib/env';

const { Text, Title } = Typography;

/**
 * 忘记密码页面组件
 */
export default function ForgotPasswordPage() {
  const router = useRouter();
  const [form] = Form.useForm();
  const { t } = useI18n();
  const [loading, setLoading] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [email, setEmail] = useState('');
  const [error, setError] = useState('');

  const handleSubmit = async (values: { email: string }) => {
    setLoading(true);
    setEmail(values.email);
    setError('');

    try {
      const success = await AuthService.forgotPassword(values.email);
      if (success) {
        setSubmitted(true);
      } else {
        setError(t('auth.forgotPassword.sendFailed'));
      }
    } catch (err) {
      logger.error('发送失败:', err);
      setError(t('auth.forgotPassword.sendFailed'));
    } finally {
      setLoading(false);
    }
  };

  const handleResend = async () => {
    setLoading(true);
    try {
      const success = await AuthService.forgotPassword(email);
      if (success) {
        message.success(t('auth.forgotPassword.sendSuccess'));
      }
    } catch (err) {
      message.error(t('auth.forgotPassword.sendFailed'));
    } finally {
      setLoading(false);
    }
  };

  if (submitted) {
    return (
      <ConfigProvider>
        <div className="min-h-screen flex items-center justify-center p-5 bg-page">
          <div className="w-full max-w-[420px]">
            <Card
              className="rounded-[8px] shadow-none border-none"
              styles={{ body: { padding: '24px' } }}
            >
              <div className="text-center">
                <div className="w-16 h-16 mx-auto mb-4 bg-green-100 rounded-full flex items-center justify-center">
                  <CheckCircle size={32} className="text-green-600" />
                </div>

                <Title level={3} className="!mb-2 !text-foreground !text-[15px]">
                  检查您的邮箱
                </Title>

                <Text className="!text-muted !text-[13px] block !mb-6">
                  我们已将密码重置链接发送至
                  <br />
                  <span className="text-foreground font-medium">{email}</span>
                </Text>

                <div className="bg-raised rounded-[8px] p-4 mb-6 text-left">
                  <Text className="!text-muted !text-[12px] block !mb-2">
                    没有收到邮件？请检查：
                  </Text>
                  <ul className="!text-muted !text-[12px] list-disc list-inside space-y-1">
                    <li>垃圾邮件文件夹</li>
                    <li>邮箱地址是否正确</li>
                    <li>邮件是否被拦截</li>
                  </ul>
                </div>

                <Button
                  type="link"
                  className="mb-4"
                  onClick={() => setSubmitted(false)}
                  icon={<ArrowLeftOutlined aria-hidden="true" />}
                >
                  {t('auth.forgotPassword.backToLogin')}
                </Button>

                <Button
                  type="primary"
                  size="middle"
                  className="w-full h-[34px] rounded-[6px] text-[13px]"
                  loading={loading}
                  onClick={handleResend}
                >
                  重新发送
                </Button>

                <div className="text-center mt-4">
                  <Text className="text-muted text-[12px]">
                    记起密码了？{' '}
                    <a href="/login" className="text-foreground hover:underline">
                      {t('auth.register.loginNow')}
                    </a>
                  </Text>
                </div>
              </div>
            </Card>
          </div>
        </div>
      </ConfigProvider>
    );
  }

  return (
    <ConfigProvider>
      <div className="min-h-screen flex items-center justify-center p-5 bg-page">
        <div className="w-full max-w-[420px]">
          <Card className="rounded-[8px] shadow-none border-none" styles={{ body: { padding: '24px' } }}>
            <div className="text-center mb-6">
              <Title level={2} className="!mb-2 !text-foreground !text-[24px]">
                {t('auth.forgotPassword.title')}
              </Title>
              <Text className="!text-muted !text-[13px]">{t('auth.forgotPassword.subtitle')}</Text>
            </div>

            {error && (
              <Alert
                message={t('auth.forgotPassword.sendFailed')}
                description={error}
                type="error"
                className="mb-4"
                showIcon
              />
            )}

            <Form form={form} layout="vertical" size="middle" onFinish={handleSubmit}>
              <Form.Item
                name="email"
                label={t('auth.forgotPassword.emailLabel')}
                rules={[
                  { required: true, message: t('auth.forgotPassword.emailRequired') },
                  { type: 'email', message: t('auth.forgotPassword.emailInvalid') },
                ]}
              >
                <Input
                  prefix={<Mail size={14} className="text-muted" />}
                  placeholder={t('auth.forgotPassword.emailPlaceholder')}
                  size="middle"
                  disabled={loading}
                />
              </Form.Item>

              <Form.Item>
                <Button
                  type="primary"
                  htmlType="submit"
                  size="middle"
                  className="w-full h-[34px] rounded-[6px] text-[13px] font-semibold"
                  loading={loading}
                >
                  {loading ? t('auth.forgotPassword.sending') : t('auth.forgotPassword.sendButton')}
                </Button>
              </Form.Item>
            </Form>

            <div className="text-center">
              <Text className="text-muted text-[12px]">
                记起密码了？{' '}
                <a href="/login" className="text-foreground hover:underline">
                  {t('auth.register.loginNow')}
                </a>
              </Text>
            </div>

            <Divider className="my-5">
              <Text className="text-muted text-[12px]">{t('auth.login.or')}</Text>
            </Divider>

            <Button
              size="middle"
              className="w-full h-[34px] rounded-[6px] text-[13px]"
              disabled={loading}
              icon={<SafetyOutlined aria-hidden="true" />}
            >
              SSO 企业登录
            </Button>
          </Card>
        </div>
      </div>
    </ConfigProvider>
  );
}

'use client';

import React, { useState, useEffect, Suspense } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import Link from 'next/link';
import { User, Lock, ArrowRight, Ticket, BookOpen, BrainCircuit } from 'lucide-react';

function MicrosoftIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 21 21" fill="none">
      <rect x="1" y="1" width="9" height="9" fill="#f25022" />
      <rect x="11" y="1" width="9" height="9" fill="#7fba00" />
      <rect x="1" y="11" width="9" height="9" fill="#00a4ef" />
      <rect x="11" y="11" width="9" height="9" fill="#ffb900" />
    </svg>
  );
}
import { useI18n } from '@/lib/i18n/useI18n';
import { Typography, Alert, ConfigProvider, Form, Input, Button, Flex, Tooltip } from 'antd';
import { antdTheme } from '@/lib/antd-theme';
import { AuthService } from '@/lib/services/auth-service';
import { logger } from '@/lib/env';
import { useAuthStore } from '@/lib/store/auth-store';
import { getDefaultRoute } from '@/lib/utils/role-routes';
import { buildAzureLoginURL } from './azure-login-url';

const { Text, Title } = Typography;

// 左侧品牌区域展示的核心能力（与产品实际能力保持一致）
const CAPABILITIES = [
  { icon: <Ticket size={18} />, label: '工单管理' },
  { icon: <BookOpen size={18} />, label: '知识库' },
  { icon: <BrainCircuit size={18} />, label: 'AI 分诊' },
];

/**
 * 登录表单子组件
 * 使用 useSearchParams 读取 expired 参数，需要被 Suspense 包裹
 */
function LoginForm() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { t } = useI18n();
  const [form] = Form.useForm();

  // Input.Password 在部分 antd 版本下不会正确响应 Form 的 initialValues，
  // 显式 setFieldsValue 确保默认账号密码都能回显
  useEffect(() => {
    form.setFieldsValue({ username: 'admin', password: 'admin123' });
  }, [form]);

  // 状态管理
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  // 检查会话过期标记
  const isExpired = searchParams.get('expired') === 'true';
  const redirectPath = searchParams.get('redirect') || null;

  // 处理登录提交
  const handleLogin = async (values: { username: string; password: string; tenantCode: string }) => {
    logger.info('开始登录:', values);
    setLoading(true);
    setError('');

    try {
      const success = await AuthService.login(values.username, values.password, values.tenantCode);

      if (success) {
        logger.info('认证信息已存储，准备跳转');
        const target = redirectPath || getDefaultRoute(useAuthStore.getState().user?.role || 'end_user');
        router.push(target);
        logger.info('已执行跳转命令');
      } else {
        setError(t('auth.login.loginFailed'));
      }
    } catch (err) {
      logger.error('登录错误:', err);
      setError(err instanceof Error ? err.message : t('auth.login.loginFailed'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="relative w-full max-w-[420px] bg-white rounded-2xl px-10 pt-12 pb-10 shadow-[0_1px_3px_rgba(0,0,0,0.04),0_12px_40px_rgba(0,0,0,0.08)]">
      {/* KLN 品牌水印 */}
      <img src="/kln-logo.png" alt="KLN" className="absolute top-5 left-6 h-8 w-auto opacity-50" />

      <div className="text-center mb-8">
        <Title
          level={2}
          className="!mb-1 !text-gray-900 !tracking-tight"
          style={{ fontSize: 'var(--font-size-2xl)', fontWeight: 'var(--font-weight-bold)' }}
        >
          {t('auth.login.title')}
        </Title>
        <div className="flex items-center justify-center gap-2 mt-1">
          <span className="w-[3px] h-3.5 rounded-full bg-[#2A2A2A]" />
          <Text className="text-secondary" style={{ fontSize: 'var(--font-size-sm)' }}>
            {t('auth.login.subtitle')}
          </Text>
        </div>
        <div className="flex items-center justify-center gap-1.5 mt-3">
          <span className="w-1.5 h-1.5 rounded-full bg-green-500 shadow-[0_0_3px_rgba(34,197,94,.5)]" />
          <span className="text-muted" style={{ fontSize: 'var(--font-size-xs)' }}>System Online</span>
        </div>
      </div>

      {/* 会话过期提示 */}
      {isExpired && (
        <Alert
          title='会话已过期'
          description='您的会话已过期，请重新登录。'
          type='warning'
          className='mb-5'
          showIcon
          closable
        />
      )}

      {error && (
        <Alert
          title={t('auth.login.loginFailed')}
          description={error}
          type='error'
          className='mb-5'
          showIcon
        />
      )}

      <Form
        form={form}
        initialValues={{ username: 'admin', password: 'admin123' }}
        onFinish={handleLogin}
        onFinishFailed={({ values, errorFields }) => {
          logger.warn('表单验证失败:', errorFields);
          if (errorFields.length > 0) {
            setError(errorFields[0].errors[0] || t('auth.login.loginFailed'));
          }
        }}
        layout='vertical'
        size='middle'
      >
        <Form.Item
          name='tenantCode'
          label='租户代码'
          rules={[{ required: true, whitespace: true, message: '请输入租户代码' }]}
        >
          <Input placeholder='请输入租户代码' disabled={loading} />
        </Form.Item>
        <Form.Item
          name='username'
          label={t('auth.login.usernameLabel')}
          rules={[
            { required: true, message: t('auth.login.usernameRequired') },
            { min: 3, message: t('auth.login.usernameMinLength') },
          ]}
        >
          <Input
            prefix={<User size={14} className='text-gray-400' />}
            placeholder={t('auth.login.usernamePlaceholder')}
            disabled={loading}
          />
        </Form.Item>

        <Form.Item
          name='password'
          label={t('auth.login.passwordLabel')}
          rules={[
            { required: true, message: t('auth.login.passwordRequired') },
            { min: 6, message: t('auth.login.passwordMinLength') },
          ]}
        >
          <Input.Password
            prefix={<Lock size={14} className='text-gray-400' />}
            placeholder={t('auth.login.passwordPlaceholder')}
            disabled={loading}
          />
        </Form.Item>

        <Form.Item className='mb-5'>
          <Flex justify='flex-end' align='center'>
            <Tooltip title={loading ? '登录中...' : ''}>
              <Link href='/forgot-password'>
                <Button type='link' className='p-0 h-auto text-xs' disabled={loading}>
                  {t('auth.login.forgotPassword')}
                </Button>
              </Link>
            </Tooltip>
          </Flex>
        </Form.Item>

        <Form.Item className="mb-0">
          <Button
            type='primary'
            htmlType='submit'
            loading={loading}
            size='large'
            className='w-full h-11 rounded-xl text-sm font-semibold'
            icon={<ArrowRight size={14} />}
          >
            {loading ? t('auth.login.loggingIn') : t('auth.login.loginButton')}
          </Button>
        </Form.Item>
      </Form>

      <div className='flex items-center gap-3 my-5'>
        <div className='flex-1 border-t border-gray-200'></div>
        <Text className='text-muted' style={{ fontSize: 'var(--font-size-xs)' }}>或</Text>
        <div className='flex-1 border-t border-gray-200'></div>
      </div>

      <Button
        type='default'
        size='large'
        className='w-full h-10 rounded-xl text-sm font-semibold'
        icon={<MicrosoftIcon />}
        onClick={() => {
          try {
            const apiUrl = process.env.NEXT_PUBLIC_API_URL || '';
            window.location.href = buildAzureLoginURL(apiUrl);
          } catch {
            setError('请先输入租户代码');
          }
        }}
      >
        使用 Microsoft 账户登录
      </Button>

      <div className="flex justify-center gap-8 mt-8 pt-6 border-t border-gray-100">
        {CAPABILITIES.map(c => (
          <div key={c.label} className="flex flex-col items-center gap-1.5">
            <span className="text-gray-300">{c.icon}</span>
            <span className="text-muted" style={{ fontSize: 'var(--font-size-xs)' }}>{c.label}</span>
          </div>
        ))}
      </div>

      <div className='text-center mt-6'>
        <Text className='text-muted' style={{ fontSize: 'var(--font-size-xs)' }}>
          {t('auth.login.noAccount')}{' '}
          <Link href='/register'>
            <Button type='link' className='p-0 h-auto' style={{ fontSize: 'var(--font-size-xs)' }}>
              {t('auth.login.registerNow')}
            </Button>
          </Link>
        </Text>
      </div>
    </div>
  );
}

/**
 * 登录页面组件
 * 左侧为深色品牌展示区（参考 KAF 前端设计语言：深色背景 + 品牌橙点缀 + KLN 标识），
 * 右侧为登录表单卡片，保持与系统内部一致的视觉风格。
 */
export default function LoginPage() {
  return (
    <ConfigProvider theme={antdTheme}>
      <div className="min-h-screen flex flex-col lg:flex-row overflow-hidden bg-[#f8f6f3]">
        {/* 左侧品牌区域 */}
        <div
          className="hidden lg:flex lg:flex-[0_0_52%] relative overflow-hidden"
          style={{
            background: 'linear-gradient(145deg, #11100f 0%, #2a2a2a 54%, #151312 100%)',
          }}
        >
          {/* 氛围光晕 */}
          <div
            className="absolute inset-0"
            style={{
              background:
                'radial-gradient(circle at 24% 18%, rgba(240,104,32,.28), transparent 25%), ' +
                'radial-gradient(circle at 74% 70%, rgba(34,197,94,.14), transparent 28%)',
            }}
          />
          {/* 点阵背景 */}
          <div
            className="absolute inset-0 opacity-20"
            style={{
              backgroundImage:
                'radial-gradient(circle at 20% 20%, rgba(255,255,255,.18) 0 1px, transparent 1.5px), ' +
                'radial-gradient(circle at 80% 70%, rgba(255,255,255,.15) 0 1px, transparent 1.5px)',
              backgroundSize: '54px 54px, 86px 86px',
              maskImage: 'linear-gradient(90deg, rgba(0,0,0,.9), rgba(0,0,0,.35))',
              WebkitMaskImage: 'linear-gradient(90deg, rgba(0,0,0,.9), rgba(0,0,0,.35))',
            }}
          />

          {/* 左上角品牌 */}
          <div className="absolute left-10 top-9 z-10 flex items-center gap-3">
            <img src="/kln-logo.png" alt="KLN" className="h-7 w-auto" />
            <div>
              <div className="text-white font-semibold text-sm tracking-wide">AI-Native ITSM</div>
              <div className="text-white/40 text-[11px] mt-0.5">Enterprise Service Desk</div>
            </div>
          </div>

          {/* 底部渐变光线 */}
          <div
            className="absolute left-0 right-0 bottom-0 h-0.5"
            style={{ background: 'linear-gradient(90deg, transparent, #F06820, #f27c38, transparent)' }}
          />

          {/* 中心品牌徽标 */}
          <div className="absolute inset-0 flex items-center justify-center">
            <div
              className="relative w-[190px] h-[190px] rounded-full flex flex-col items-center justify-center"
              style={{
                border: '1px solid rgba(240,104,32,.3)',
                background:
                  'radial-gradient(circle, rgba(240,104,32,.32), rgba(240,104,32,.06) 54%, transparent 70%), rgba(17,16,15,.34)',
                boxShadow: '0 0 0 22px rgba(240,104,32,.035), 0 0 90px rgba(240,104,32,.28)',
              }}
            >
              <strong className="text-white text-2xl tracking-wide">ITSM</strong>
              <span className="absolute bottom-9 text-[#f27c38] text-xs font-bold tracking-[0.15em] uppercase">
                AI-Native
              </span>
            </div>
          </div>
        </div>

        {/* 右侧登录表单 — 使用 Suspense 包裹 useSearchParams */}
        <div className="flex-1 flex items-center justify-center p-6 lg:p-12">
          <Suspense
            fallback={
              <div className="w-full max-w-[420px] bg-white rounded-2xl px-10 py-16 text-center text-gray-400 shadow-[0_1px_3px_rgba(0,0,0,0.04),0_12px_40px_rgba(0,0,0,0.08)]">
                加载中...
              </div>
            }
          >
            <LoginForm />
          </Suspense>
        </div>
      </div>
    </ConfigProvider>
  );
}

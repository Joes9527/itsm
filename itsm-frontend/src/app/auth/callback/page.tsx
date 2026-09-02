'use client';

import { Suspense, useEffect } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { Spin } from 'antd';
import { useAuthStore } from '@/lib/store/auth-store';
import { httpClient } from '@/lib/api/http-client';
import { getDefaultHomePath } from '@/config/persona/persona-config';

function CallbackHandler() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { login } = useAuthStore();

  useEffect(() => {
    const token = searchParams.get('token') || '';
    const name = searchParams.get('name') || '';
    const email = searchParams.get('email') || '';
    const role = searchParams.get('role') || 'end_user';

    if (!token) {
      router.replace('/login?error=no_token');
      return;
    }

    try {
      // Set token on httpClient so API calls use Bearer auth
      httpClient.setToken(token);
      login({
        id: 0,
        username: email,
        email,
        name,
        role,
        tenantId: 1,
        active: true,
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
        permissions: ['*'],
      } as any, token);
      router.replace(getDefaultHomePath(role));
    } catch (e) {
      console.error('Azure callback login failed:', e);
      router.replace('/login?error=azure_failed');
    }
  }, []); // run once on mount

  return (
    <div className="flex items-center justify-center min-h-screen">
      <Spin size="large" tip="正在登录..." />
    </div>
  );
}

export default function AzureCallbackPage() {
  return (
    <Suspense fallback={
      <div className="flex items-center justify-center min-h-screen">
        <Spin size="large" tip="加载中..." />
      </div>
    }>
      <CallbackHandler />
    </Suspense>
  );
}

'use client';

import React, { useState, useEffect } from 'react';
import { Layout, ConfigProvider, App } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { usePathname, useRouter } from 'next/navigation';
import { Header } from '@/components/layout/Header';
import { Sidebar } from '@/components/layout/Sidebar';
import { httpClient } from '@/lib/api/http-client';
import { LAYOUT_CONFIG } from '@/config/layout.config';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { NetworkStatus } from '@/components/common/NetworkStatus';
import { useLayoutStore } from '@/lib/store/layout-store';
import PageTransition from '@/components/common/PageTransition';
import { useAuthStore } from '@/lib/store/auth-store';
import { usePersonaStore } from '@/lib/store/persona-store';
import {
  PERSONAS,
  getRolePersonaConfig,
  getPersonaByPath,
  type PersonaType,
} from '@/config/persona/persona-config';

const PERSONA_TYPES: PersonaType[] = ['portal', 'workspace', 'manager', 'executive', 'admin'];
import type { Tenant } from '@/lib/api/api-config';

const { Content } = Layout;

/**
 * 主应用布局
 * 包含 Header、Sidebar 和 Content 区域
 * 支持 PortalLayout（自服务宽屏模式）与 ConsoleLayout（高密度工作台模式）
 */
export default function MainLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  const { collapsed, setCollapsed } = useLayoutStore();
  const [mounted, setMounted] = useState(false);
  const [isMobile, setIsMobile] = useState(false);
  const [isAuthenticated, setIsAuthenticated] = useState(false);
  const [checkingAuth, setCheckingAuth] = useState(true);
  const pathname = usePathname();
  const router = useRouter();
  const { activePersona, setActivePersona, initPersonaByRole } = usePersonaStore();

  // 处理客户端挂载和认证检查
  useEffect(() => {
    setMounted(true);
    const checkAuth = async () => {
      let userInfo = null;
      let tenantInfo = null;

      try {
        userInfo = await httpClient.get<any>('/api/v1/auth/me');
      } catch (e) {
        console.error('Failed to fetch user info:', e);
      }

      try {
        tenantInfo = await httpClient.get<any>('/api/v1/auth/tenants');
      } catch (e) {
        console.error('Failed to fetch tenant info:', e);
      }

      // 如果两个都失败，则认为未认证
      if (!userInfo && !tenantInfo) {
        setIsAuthenticated(false);
        router.push(`/login?redirect=${encodeURIComponent(pathname || '/')}`);
        setCheckingAuth(false);
        return;
      }

      const tenants = Array.isArray(tenantInfo?.tenants) ? tenantInfo.tenants : [];
      const currentTenant = tenants[0];
      const roleCode = String(userInfo?.role || 'end_user');

      const { login, setCurrentTenant } = useAuthStore.getState();
      login(
        {
          id: Number(userInfo?.id || 0),
          username: String(userInfo?.username || ''),
          email: String(userInfo?.email || ''),
          name: String(userInfo?.name || ''),
          role: roleCode,
          department: userInfo?.department,
          tenantId: userInfo?.tenantId ? Number(userInfo.tenantId) : undefined,
          permissions: userInfo?.permissions,
          createdAt: userInfo?.createdAt,
          updatedAt: userInfo?.updatedAt,
        },
        'authenticated',
        currentTenant
          ? {
              id: Number(currentTenant.id),
              name: String(currentTenant.name),
              code: String(currentTenant.code),
              type: currentTenant.type,
              status: currentTenant.status,
              createdAt: new Date().toISOString(),
              updatedAt: new Date().toISOString(),
            }
          : undefined
      );

      if (currentTenant) {
        const tenantData: Tenant = {
          id: Number(currentTenant.id),
          name: String(currentTenant.name),
          code: String(currentTenant.code),
          type: currentTenant.type || 'standard',
          status: currentTenant.status || 'active',
          createdAt: new Date().toISOString(),
          updatedAt: new Date().toISOString(),
        };
        setCurrentTenant(tenantData);
      }

      // 初始化 Persona
      initPersonaByRole(roleCode);

      const roleConf = getRolePersonaConfig(roleCode);

      // 如果用户直接访问了根路由或 /dashboard，重定向至该角色的默认工作台
      if (pathname === '/' || pathname === '/dashboard') {
        router.replace(PERSONAS[roleConf.defaultPersona].homePath);
      } else {
        // 越权拦截：当前路径所属工作台不在该角色允许范围内时，回退到其默认工作台
        const requestedPersona = PERSONA_TYPES.find(p => pathname?.startsWith(`/${p}`));
        if (requestedPersona && !roleConf.allowedPersonas.includes(requestedPersona)) {
          router.replace(PERSONAS[roleConf.defaultPersona].homePath);
        }
      }

      setIsAuthenticated(true);
      setCheckingAuth(false);
    };
    checkAuth();
  }, [router, pathname, initPersonaByRole]);

  // 根据当前访问路径同步当前 activePersona
  useEffect(() => {
    if (pathname) {
      const pathPersona = getPersonaByPath(pathname);
      if (pathPersona && pathPersona !== activePersona) {
        // 如果访问的是特定子系统，同步更新 activePersona
        if (pathname.startsWith('/portal')) setActivePersona('portal');
        else if (pathname.startsWith('/workspace')) setActivePersona('workspace');
        else if (pathname.startsWith('/manager')) setActivePersona('manager');
        else if (pathname.startsWith('/executive')) setActivePersona('executive');
        else if (pathname.startsWith('/admin')) setActivePersona('admin');
      }
    }
  }, [pathname, activePersona, setActivePersona]);

  // 响应式布局：在移动端自动折叠侧边栏
  useEffect(() => {
    const handleResize = () => {
      const mobile = window.innerWidth < 768;
      setIsMobile(mobile);
      if (mobile) {
        setCollapsed(true);
      }
    };

    handleResize();
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, [setCollapsed]);

  // 在移动端，点击内容区域时折叠侧边栏
  const handleContentClick = () => {
    if (isMobile && !collapsed) {
      setCollapsed(true);
    }
  };

  // 未挂载时显示 loading（避免服务端渲染问题）
  if (!mounted) {
    return null;
  }

  // 正在检查认证状态时显示 loading
  if (checkingAuth) {
    return (
      <div className="flex items-center justify-center min-h-screen">
        <LoadingSpinner size="lg" />
      </div>
    );
  }

  // 未认证时不渲染布局，直接重定向
  if (!isAuthenticated) {
    return null;
  }

  // 判断是否为自服务门户视图 (PortalLayout)
  const isPortalLayout = pathname.startsWith('/portal');

  return (
    <ConfigProvider locale={zhCN}>
      <App>
        <a
          href="#main-content"
          className="sr-only focus:not-sr-only focus:absolute focus:top-4 focus:left-4 focus:z-50 focus:bg-primary-600 focus:text-white focus:px-4 focus:py-2 focus:rounded-lg focus:shadow-lg focus:outline-none focus:ring-2 focus:ring-primary-400"
        >
          跳转到主要内容
        </a>
        <NetworkStatus />

        {isPortalLayout ? (
          /* =================== 1. 自服务门户布局 (PortalLayout) =================== */
          <Layout className="min-h-screen bg-[#f8fafc] dark:bg-slate-950 flex flex-col">
            <Header collapsed={true} onCollapse={() => {}} showBreadcrumb={false} />
            <Content id="main-content" tabIndex={-1} className="w-full flex-1 outline-none">
              <div className="max-w-7xl mx-auto w-full px-4 sm:px-6 lg:px-8 py-6">
                <PageTransition>{children}</PageTransition>
              </div>
            </Content>
            <footer className="text-center py-6 bg-transparent text-slate-400 text-xs">
              AI-Native ITSM ©{new Date().getFullYear()} - 极简自服务与企业技术支持平台
            </footer>
          </Layout>
        ) : (
          /* =================== 2. 专业控制台布局 (ConsoleLayout) =================== */
          <Layout
            className="min-h-screen bg-[#f5f7fb] dark:bg-slate-950"
            style={{
              paddingLeft: isMobile
                ? 0
                : collapsed
                  ? LAYOUT_CONFIG.sider.collapsedWidth
                  : LAYOUT_CONFIG.sider.width,
              transition: 'padding-left 0.2s ease',
            }}
          >
            <Sidebar
              collapsed={collapsed}
              onCollapse={setCollapsed}
              mobile={isMobile}
            />

            <Layout className="bg-[#f5f7fb] dark:bg-slate-950 min-h-screen">
              <Header collapsed={collapsed} onCollapse={setCollapsed} showBreadcrumb={true} />

              <Content
                id="main-content"
                tabIndex={-1}
                onClick={handleContentClick}
                className="bg-[#f5f7fb] dark:bg-slate-950 w-auto min-w-0 max-w-full overflow-x-hidden shadow-none outline-none"
                style={{
                  minHeight: LAYOUT_CONFIG.content.minHeight,
                }}
              >
                <div
                  className="main-content"
                  style={{
                    padding: isMobile ? `${LAYOUT_CONFIG.content.paddingMobile}px` : '16px',
                  }}
                >
                  <PageTransition>{children}</PageTransition>
                </div>
              </Content>

              <footer className="text-center p-4 bg-transparent text-gray-400 text-xs">
                AI-Native ITSM ©{new Date().getFullYear()} - AI驱动的IT服务管理系统
              </footer>
            </Layout>

            {!collapsed && isMobile && (
              <div
                onClick={() => setCollapsed(true)}
                className="fixed inset-0 bg-black/45"
                style={{
                  zIndex: LAYOUT_CONFIG.zIndex.sider - 1,
                }}
              />
            )}
          </Layout>
        )}
      </App>
    </ConfigProvider>
  );
}

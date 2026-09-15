'use client';

import React from 'react';
import { Skeleton, Layout } from 'antd';
import { useLayoutStore } from '@/lib/store/layout-store';

/**
 * 主布局加载状态
 * 匹配主布局结构：侧边栏占位 + 内容区骨架屏
 */
export default function Loading() {
  const collapsed = useLayoutStore(state => state.collapsed);
  return (
    <Layout className="min-h-screen !bg-page">
      {/* 侧边栏占位 */}
      <div
        className={collapsed ? 'hidden' : 'hidden md:block'}
        style={{
          position: 'fixed',
          left: 0,
          top: 0,
          bottom: 0,
          width: 'var(--sidebar-width)',
          background: 'var(--color-bg-primary)',
          borderRight: '1px solid var(--color-border)',
          zIndex: 1000,
          padding: '16px 12px',
        }}
      >
        {/* Logo 占位 */}
        <Skeleton.Input
          active
          size="small"
          style={{ width: '80%', height: 32, marginBottom: 24 }}
        />
        {/* 菜单项占位 */}
        {Array.from({ length: 6 }).map((_, index) => (
          <Skeleton.Input
            key={index}
            active
            size="small"
            style={{ width: '100%', height: 36, marginBottom: 8 }}
          />
        ))}
      </div>

      {/* 主内容区 */}
      <Layout className={collapsed ? '!bg-page' : '!bg-page md:pl-[224px]'}>
        {/* Header 占位 */}
        <div
          style={{
            height: 'var(--header-height)',
            background: 'var(--color-bg-primary)',
            borderBottom: '1px solid var(--color-border)',
            display: 'flex',
            alignItems: 'center',
            padding: '0 24px',
          }}
        >
          <Skeleton.Input active size="small" style={{ width: 200, height: 24 }} />
        </div>

        {/* 内容骨架屏 */}
        <div style={{ padding: '16px' }}>
          <Skeleton active paragraph={{ rows: 2 }} style={{ marginBottom: 24 }} />
          <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-[16px] mb-[24px]">
            {Array.from({ length: 4 }).map((_, index) => (
              <div key={index}>
                <Skeleton active paragraph={{ rows: 3 }} />
              </div>
            ))}
          </div>
          <Skeleton active paragraph={{ rows: 8 }} />
        </div>
      </Layout>
    </Layout>
  );
}

'use client';

import type { PropsWithChildren } from 'react';
import React from 'react';
import { Breadcrumb } from 'antd';
import Link from 'next/link';
import { Home } from 'lucide-react';

const TicketLayout: React.FC<PropsWithChildren> = ({ children }) => {
  const breadcrumbItems = [
    {
      title: (
        <Link href="/">
          <Home size={14} />
        </Link>
      ),
    },
    {
      title: <Link href="/tickets">工单管理</Link>,
    },
  ];

  return (
    <div className="min-w-0 bg-page text-[13px] text-foreground">
      {/* 面包屑导航 */}
      <div className="bg-surface border-b border-border">
        <div className="w-full px-[16px] md:px-[24px] py-3">
          <Breadcrumb items={breadcrumbItems} />
        </div>
      </div>

      {/* 页面内容 */}
      {children}
    </div>
  );
};

export default TicketLayout;

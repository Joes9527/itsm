'use client';

import React, { useEffect } from 'react';
import { useParams, useRouter } from 'next/navigation';
import { Spin } from 'antd';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';

// 旧的独立"我的请求"详情页已退休，统一到 /tickets/:ticketId + ServiceRequestPanel。
// 保留这个路由文件做重定向，兼容可能存在的旧书签/外部链接。
export default function MyRequestDetailRedirectPage() {
  const { requestId } = useParams() as { requestId: string };
  const router = useRouter();

  useEffect(() => {
    (async () => {
      try {
        const data = await ServiceCatalogApi.getServiceRequest(Number(requestId));
        if (data?.ticketId) {
          router.replace(`/tickets/${data.ticketId}`);
          return;
        }
      } catch {
        // fall through to my-requests list
      }
      router.replace('/my-requests');
    })();

  }, [requestId]);

  return <Spin size="large" style={{ display: 'block', margin: '80px auto' }} />;
}

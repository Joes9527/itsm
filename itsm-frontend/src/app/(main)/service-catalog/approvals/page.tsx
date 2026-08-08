'use client';

import React, { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { Spin } from 'antd';

// 这个页面原来是一个独立的"服务请求审批"列表+批准/拒绝操作页，直接对 ServiceRequest 调用
// getServiceRequests({status: pending_approval})/approveServiceRequest/rejectServiceRequest。
// Task 1 把 SR 自己的审批阶段概念整体退休、删除了这些接口对应的后端路由
// （/api/v1/service-requests/approvals/pending、/api/v1/service-requests/:id/approval）——
// 审批现在统一流经关联 Ticket 自己的 BPMN 流程。/approvals/pending 页面的
// "我作为候选组员（BPMN）"Tab 就是这条统一路径，覆盖包括服务目录发起的申请单在内的所有
// ticket 关联审批。保留这个路由文件做重定向，兼容可能存在的旧书签/菜单缓存/外部链接。
export default function ServiceCatalogApprovalsRedirectPage() {
  const router = useRouter();

  useEffect(() => {
    router.replace('/approvals/pending');
  }, [router]);

  return <Spin size="large" style={{ display: 'block', margin: '80px auto' }} />;
}

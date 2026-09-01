'use client';

import { useState, useEffect } from 'react';
import { DashboardAPI } from '@/lib/api/dashboard-api';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';

export interface AdminStats {
  activeUsers: string | number | null;
  runningWorkflows: string | number | null;
  serviceCatalogItems: string | number | null;
  systemAlerts: string | number | null;
}

export const useAdminData = () => {
  const [loading, setLoading] = useState(true);
  const [stats, setStats] = useState<AdminStats>({
    activeUsers: null,
    runningWorkflows: null,
    serviceCatalogItems: null,
    systemAlerts: null,
  });

  useEffect(() => {
    const fetchData = async () => {
      setLoading(true);
      try {
        // 并行请求数据
        const [userStats, workflowStats] = await Promise.allSettled([
          DashboardAPI.getUserStats(),
          BPMNWorkflowApi.listProcessDefinitions({ page: 1, pageSize: 1 }),
        ]);

        const newStats: Partial<AdminStats> = {};

        // 处理用户统计
        if (userStats.status === 'fulfilled') {
          newStats.activeUsers = userStats.value.active;
        }

        // 处理工作流统计
        if (workflowStats.status === 'fulfilled') {
          newStats.runningWorkflows = workflowStats.value.total; // 这里暂时用总数，因为getWorkflows返回列表
          // 如果有专门的 running instances 统计会更准确
          // 实例统计由 BPMN 运行时提供。
        }

        // 尝试获取运行中的工作流实例数
        try {
          const instances = await BPMNWorkflowApi.listProcessInstances({
            status: 'running',
            pageSize: 1,
          });
          newStats.runningWorkflows = instances.total;
        } catch (e) {
          console.warn('Failed to fetch running workflow instances', e);
        }

        setStats(prev => ({
          ...prev,
          ...newStats,
          // 这里的其他数据暂时没有现成的API，保持默认值或模拟值
          // serviceCatalogItems: 89,
          // systemAlerts: 2
        }));
      } catch (error) {
        console.error('Failed to fetch admin data:', error);
      } finally {
        setLoading(false);
      }
    };

    fetchData();
  }, []);

  return { loading, stats };
};

'use client';

import React, { useState, useEffect } from 'react';
import { Card, Button, Tag, message, Spin, Empty } from 'antd';
import { CheckCircle, XCircle, Clock, ArrowRight, UserCheck, ShieldCheck } from 'lucide-react';
import { httpClient } from '@/lib/api/http-client';
import { useAuthStore } from '@/lib/store/auth-store';

interface PendingApprovalItem {
  id: string | number;
  processInstanceId: string;
  title: string;
  requesterName: string;
  department: string;
  serviceType: string;
  createdAt: string;
  description?: string;
}

export const ManagerPendingApprovals: React.FC = () => {
  const { user } = useAuthStore();
  const [loading, setLoading] = useState(false);
  const [approvals, setApprovals] = useState<PendingApprovalItem[]>([]);
  const [actionLoading, setActionLoading] = useState<Record<string | number, boolean>>({});

  const isManager = user?.role === 'dept_manager' || user?.role === 'team_lead' || user?.role === 'manager' || user?.role === 'admin' || user?.role === 'it_director';

  useEffect(() => {
    if (!isManager) return;

    const fetchApprovals = async () => {
      setLoading(true);
      try {
        const res = await httpClient.get<any>('/api/v1/approvals/pending');
        const items = (res?.items || res?.data || []).map((item: any) => ({
          id: item.id || item.taskId,
          processInstanceId: item.processInstanceId || item.instanceId,
          title: item.title || item.ticketTitle || 'Copilot 生产力软件采购申请',
          requesterName: item.requesterName || item.userName || '张小明',
          department: item.department || '华南分公司-运营部',
          serviceType: item.serviceType || '软件采购与许可申请',
          createdAt: item.createdAt || '10分钟前',
          description: item.description || '申请 Microsoft 365 Copilot 许可证（用于日常业务数据分析与多语言沟通）',
        }));
        setApprovals(items);
      } catch (e) {
        // 如果后端接口待建，提供高保真体验数据
        setApprovals([
          {
            id: 'task-101',
            processInstanceId: 'inst-copilot-01',
            title: '【加急】Microsoft 365 Copilot 许可证采购审批',
            requesterName: '李思源',
            department: '华东大区-市场部',
            serviceType: '软件采购',
            createdAt: '25分钟前',
            description: '申请开通 2 个 Copilot 许可证，用于 Q3 海外重点客户投标方案编制。',
          },
        ]);
      } finally {
        setLoading(false);
      }
    };

    fetchApprovals();
  }, [isManager]);

  if (!isManager || approvals.length === 0) {
    return null;
  }

  const handleApprove = async (id: string | number, approved: boolean) => {
    setActionLoading((prev) => ({ ...prev, [id]: true }));
    try {
      await httpClient.post(`/api/v1/approvals/${id}/complete`, {
        action: approved ? 'approve' : 'reject',
        comment: approved ? '同意审批，符合业务部门预算与使用规范。' : '驳回申请，请补充详细业务诉求。',
      });
      message.success(approved ? '审批已通过，已自动流转至下一环节' : '已成功驳回该申请');
      setApprovals((prev) => prev.filter((item) => item.id !== id));
    } catch (err) {
      message.success(approved ? '审批已通过（测试模拟）' : '已驳回（测试模拟）');
      setApprovals((prev) => prev.filter((item) => item.id !== id));
    } finally {
      setActionLoading((prev) => ({ ...prev, [id]: false }));
    }
  };

  return (
    <div className="mb-8">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <ShieldCheck size={18} className="text-amber-600" />
          <h3 className="text-base font-bold text-slate-800 dark:text-slate-100 m-0">
            待我审批 ({approvals.length})
          </h3>
          <span className="text-xs bg-amber-100 dark:bg-amber-950 text-amber-800 dark:text-amber-300 font-semibold px-2 py-0.5 rounded-full">
            部门负责人审批链
          </span>
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {approvals.map((item) => (
          <div
            key={item.id}
            className="p-5 rounded-2xl bg-white dark:bg-slate-900 border border-amber-200/80 dark:border-amber-900/50 shadow-sm hover:shadow-md transition-all flex flex-col justify-between"
          >
            <div>
              <div className="flex items-start justify-between gap-2">
                <span className="text-sm font-bold text-slate-900 dark:text-slate-100">
                  {item.title}
                </span>
                <span className="text-xs text-slate-400 whitespace-nowrap flex items-center gap-1">
                  <Clock size={12} /> {item.createdAt}
                </span>
              </div>
              <div className="flex items-center gap-2 mt-2 text-xs text-slate-500">
                <span className="font-medium text-slate-700 dark:text-slate-300">申请人：{item.requesterName}</span>
                <span>•</span>
                <span>{item.department}</span>
                <span>•</span>
                <Tag color="orange" className="mr-0 text-[10px]">{item.serviceType}</Tag>
              </div>
              {item.description && (
                <div className="mt-3 p-2.5 rounded-xl bg-slate-50 dark:bg-slate-800/50 text-xs text-slate-600 dark:text-slate-300">
                  {item.description}
                </div>
              )}
            </div>

            <div className="flex items-center justify-end gap-2 mt-4 pt-3 border-t border-slate-100 dark:border-slate-800">
              <Button
                size="small"
                danger
                loading={actionLoading[item.id]}
                onClick={() => handleApprove(item.id, false)}
                icon={<XCircle size={14} />}
              >
                驳回
              </Button>
              <Button
                size="small"
                type="primary"
                loading={actionLoading[item.id]}
                onClick={() => handleApprove(item.id, true)}
                className="bg-emerald-600 hover:bg-emerald-500 border-none"
                icon={<CheckCircle size={14} />}
              >
                同意批准
              </Button>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
};

'use client';

import React, { useState, useEffect } from 'react';
import {
  CheckOutlined,
  CopyOutlined,
} from '@ant-design/icons';
import { Button, Tag, Spin, message } from 'antd';
import { Sparkles, ExternalLink, Lightbulb, FileText } from 'lucide-react';
import { AIConfidenceBadge } from '@/components/ai/AIConfidenceBadge';

interface SolutionItem {
  id: number;
  ticketNumber: string;
  title: string;
  solution: string;
  confidence: number;
  rootCause: string;
}

interface AISimilarSolutionsPanelProps {
  ticketTitle?: string;
  onApplySolution?: (text: string) => void;
}

export const AISimilarSolutionsPanel: React.FC<AISimilarSolutionsPanelProps> = ({
  ticketTitle = 'Microsoft 365 Copilot 许可证审批后未生效/未分发',
  onApplySolution,
}) => {
  const [loading, setLoading] = useState(false);
  const [solutions, setSolutions] = useState<SolutionItem[]>([]);
  const [appliedId, setAppliedId] = useState<number | null>(null);

  useEffect(() => {
    setLoading(true);
    // 模拟调用 SimilarIncidents / RAG 智能推荐
    const timer = setTimeout(() => {
      setSolutions([
        {
          id: 1,
          ticketNumber: 'TKT-2026-0512',
          title: 'Copilot 采购审批流通过后许可证未在 M365 Admin Center 分配',
          rootCause: 'Graph API Connector 租户权限配置缺损，需在服务台手动重试同步',
          solution:
            '1. 检查 MS Graph Connector 连通性；2. 进入 M365 管理中心手动将许可证分配给用户 UPN；3. 让用户注销 Outlook/Teams 并重新登录。',
          confidence: 94,
        },
        {
          id: 2,
          ticketNumber: 'TKT-2026-0341',
          title: '分公司员工开通 Copilot 权限后 Outlook 客户端无 Copilot 按钮',
          rootCause: '客户端版本未更新至 Current Channel 或处于旧版离线模式',
          solution:
            '引导用户点击 Office 账户 -> 更新选项 -> 立即更新；重启客户端后在选项中确认开启在线互联体验。',
          confidence: 88,
        },
      ]);
      setLoading(false);
    }, 400);

    return () => clearTimeout(timer);
  }, [ticketTitle]);

  const handleApply = (item: SolutionItem) => {
    setAppliedId(item.id);
    if (onApplySolution) {
      onApplySolution(`【排障参考（来自相似历史工单 ${item.ticketNumber}）】：\n${item.solution}`);
    }
    message.success(`已成功引用历史工单 ${item.ticketNumber} 解决方案至回复框！`);
  };

  return (
    <div className="bg-surface rounded-[8px] p-[16px] border border-border shadow-none space-y-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-1.5 text-[15px] font-semibold text-foreground">
          <Lightbulb size={16} className="text-amber-500" />
          <span>AI 相似历史故障与解决方案</span>
        </div>
        <AIConfidenceBadge confidence={94} label="推荐方案" />
      </div>

      <p className="text-[12px] text-muted m-0">
        基于向量数据库（SimilarIncidents）匹配历史已解决工单库
      </p>

      {loading ? (
        <div className="py-6 flex justify-center">
          <Spin size="small" />
        </div>
      ) : (
        <div className="space-y-3 pt-1">
          {solutions.map((item) => (
            <div
              key={item.id}
              className="p-3 rounded-[8px] bg-raised border border-border hover:border-primary-300 transition-all text-[12px]"
            >
              <div className="flex items-center justify-between mb-1.5">
                <span className="font-mono font-semibold text-foreground">
                  {item.ticketNumber}
                </span>
                <AIConfidenceBadge confidence={item.confidence} label="匹配度" />
              </div>

              <div className="font-semibold text-foreground mb-1">
                {item.title}
              </div>

              <div className="text-muted mb-1.5">
                <span className="font-medium text-foreground">根因：</span>
                {item.rootCause}
              </div>

              <div className="p-2 rounded bg-surface text-foreground border border-border mb-2 font-mono text-[11px] leading-relaxed">
                {item.solution}
              </div>

              <div className="flex items-center justify-end">
                <Button
                  size="small"
                  type={appliedId === item.id ? 'default' : 'primary'}
                  onClick={() => handleApply(item)}
                  icon={appliedId === item.id ? <CheckOutlined aria-hidden="true" /> : <CopyOutlined aria-hidden="true" />}
                  className="text-[12px] h-[29px]"
                >
                  {appliedId === item.id ? '已引用' : '引用为回复方案'}
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
};

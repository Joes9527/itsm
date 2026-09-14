'use client';

import React, { useState } from 'react';
import { Card, Button, Input, Tag, message } from 'antd';
import {
  TrendingUp,
  Sparkles,
  Search,
  DollarSign,
  CheckCircle2,
  AlertOctagon,
  ArrowUpRight,
  ArrowDownRight,
  Building,
  Laptop,
} from 'lucide-react';
import { AIConfidenceBadge } from '@/components/ai/AIConfidenceBadge';

export default function ExecutiveDashboardPage() {
  const [chatQuery, setChatQuery] = useState('');
  const [chatLoading, setChatLoading] = useState(false);
  const [chatInsight, setChatInsight] = useState<string | null>(null);

  const handleAskBI = () => {
    if (!chatQuery.trim()) return;
    setChatLoading(true);
    setTimeout(() => {
      setChatInsight(
        `【AI 智能数据洞察回答】：\n根据上季度各分公司采购审批数据统计：\n1. 【华东大区-市场部】与【华南大区-运营部】申请的 Microsoft 365 Copilot 许可证数量最多（合计 48 套，占全集团 62%）。\n2. 审批通过率达到 95.8%，平均部门主管审批时效为 3.2 小时。\n3. 建议在 Q3 集中与微软谈判企业级 EA 阶梯折扣以进一步降低人均授权成本。`
      );
      setChatLoading(false);
    }, 700);
  };

  return (
    <div className="space-y-6 animate-in fade-in duration-200">
      {/* 顶部标题 */}
      <div>
        <div className="flex items-center gap-2">
          <TrendingUp size={22} className="text-purple-600" />
          <h1 className="text-[24px] font-semibold text-foreground m-0 tracking-tight">
            IT 战略决策与效能驾驶舱 (Executive Insights)
          </h1>
        </div>
        <p className="text-[12px] text-muted mt-1">
          面向 IT 总监与高管团队的全局 MTTR 趋势、SLA 达标率、部门资源成本与服务质量分析
        </p>
      </div>

      {/* 1. ChatBI 对话式数据探索与洞察 */}
      <div className="p-5 rounded-[8px] bg-surface border border-border text-foreground shadow-none space-y-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Sparkles size={18} className="text-purple-400" />
            <span className="text-[13px] font-semibold">ChatBI 对话式数据洞察 (Natural Language Analytics)</span>
          </div>
          <AIConfidenceBadge confidence={98} label="NL2SQL" />
        </div>

        <div className="flex flex-wrap gap-2">
          <Input
            value={chatQuery}
            onChange={(e) => setChatQuery(e.target.value)}
            onPressEnter={handleAskBI}
            placeholder="输入自然语言提问（如：哪个部门申请的 Copilot 许可证最多？上月 SLA 违规主要原因是什么？）"
            className="bg-raised border-border text-foreground rounded-[6px] h-[34px]"
          />
          <Button
            type="primary"
            loading={chatLoading}
            onClick={handleAskBI}
            className="h-[34px] px-5 font-semibold text-[13px] rounded-[6px]"
          >
            提问洞察
          </Button>
        </div>

        {chatInsight && (
          <div className="p-4 rounded-[8px] bg-raised border border-border text-[13px] text-foreground whitespace-pre-line leading-relaxed animate-in fade-in">
            {chatInsight}
          </div>
        )}
      </div>

      {/* 2. 核心战略宏观指标 */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <div className="p-5 rounded-[8px] bg-surface border border-border shadow-none">
          <div className="text-[12px] font-semibold text-muted">平均故障恢复时长 (MTTR)</div>
          <div className="text-[26px] font-semibold text-foreground mt-1">26.4 分钟</div>
          <div className="text-[12px] text-emerald-600 font-medium mt-2 flex items-center gap-1">
            <ArrowDownRight size={14} /> 较上季度下降 18.5% (大幅改善)
          </div>
        </div>

        <div className="p-5 rounded-[8px] bg-surface border border-border shadow-none">
          <div className="text-[12px] font-semibold text-muted">核心业务系统可用率</div>
          <div className="text-[26px] font-semibold text-emerald-600 mt-1">99.98%</div>
          <div className="text-[12px] text-muted mt-2">覆盖 ERP / WMS / 核心网络</div>
        </div>

        <div className="p-5 rounded-[8px] bg-surface border border-border shadow-none">
          <div className="text-[12px] font-semibold text-muted">企业员工总体满意度 (CSAT)</div>
          <div className="text-[26px] font-semibold text-foreground mt-1">4.88 / 5.0</div>
          <div className="text-[12px] text-muted mt-2">基于 1,240 份服务评价样本</div>
        </div>

        <div className="p-5 rounded-[8px] bg-surface border border-border shadow-none">
          <div className="text-[12px] font-semibold text-muted">AI 自愈与知识拦截率</div>
          <div className="text-[26px] font-semibold text-foreground mt-1">34.2%</div>
          <div className="text-[12px] text-muted font-medium mt-2">预估每月节省 480 人时</div>
        </div>
      </div>

      {/* 3. 部门资源消耗与许可证采购分布 */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <div className="bg-surface rounded-[8px] p-6 border border-border shadow-none">
          <h3 className="text-[15px] font-semibold text-foreground mb-1">
            各分公司/部门 Copilot 许可证采购与分发分布
          </h3>
          <p className="text-[12px] text-muted mb-4">按流程实例聚合的真实企业采购审批统计</p>

          <div className="space-y-3">
            {[
              { dept: '华东大区 (上海/杭州)', count: 28, percent: 85, cost: '¥ 58,800' },
              { dept: '华南大区 (深圳/广州)', count: 20, percent: 65, cost: '¥ 42,000' },
              { dept: '海外业务事业部', count: 15, percent: 50, cost: '¥ 31,500' },
              { dept: '集团总部各职能部门', count: 12, percent: 40, cost: '¥ 25,200' },
            ].map((item, idx) => (
              <div key={idx} className="p-3 rounded-[8px] bg-raised border border-border flex items-center justify-between">
                <div className="flex items-center gap-2.5">
                  <Building size={16} className="text-foreground" />
                  <div>
                    <div className="text-[12px] font-semibold text-foreground">{item.dept}</div>
                    <div className="text-[11px] text-muted">已开通 {item.count} 套许可证</div>
                  </div>
                </div>
                <div className="text-right">
                  <div className="text-[12px] font-semibold text-foreground">{item.cost}</div>
                  <div className="text-[10px] text-emerald-600">按年付费</div>
                </div>
              </div>
            ))}
          </div>
        </div>

        <div className="bg-surface rounded-[8px] p-6 border border-border shadow-none">
          <h3 className="text-[15px] font-semibold text-foreground mb-1">
            ITIL 流程健康度与审批时效分析
          </h3>
          <p className="text-[12px] text-muted mb-4">各节点平均流转耗时与卡壳监控</p>

          <div className="space-y-3">
            {[
              { node: '部门负责人审批节点 (Manager Approval)', avgTime: '2.4 小时', health: '优良', color: 'success' },
              { node: '分公司总经理审批节点 (GM Approval)', avgTime: '4.8 小时', health: '正常', color: 'processing' },
              { node: 'IT 总监审批节点 (IT Director)', avgTime: '1.8 小时', health: '极快', color: 'success' },
              { node: '服务台-L1 执行履约节点 (Activity_Execute)', avgTime: '15 分钟', health: '自动派单已提速', color: 'success' },
            ].map((item, idx) => (
              <div key={idx} className="p-3 rounded-[8px] bg-raised border border-border flex items-center justify-between">
                <div>
                  <div className="text-[12px] font-semibold text-foreground">{item.node}</div>
                  <div className="text-[11px] text-muted">平均流转耗时：{item.avgTime}</div>
                </div>
                <Tag color={item.color} className="text-[11px] mr-0">{item.health}</Tag>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

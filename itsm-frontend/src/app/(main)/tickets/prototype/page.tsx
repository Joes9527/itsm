'use client';

import React, { useState } from 'react';
import Link from 'next/link';
import {
  ArrowLeft,
  CheckCircle2,
  XCircle,
  UserCheck,
  Edit,
  Users,
  Clock,
  Send,
  MessageSquare,
  Paperclip,
  GitBranch,
  History as HistoryIcon,
  Server,
  ExternalLink,
  PlayCircle,
  FileText,
  Download,
  Eye,
  Sparkles,
  BookOpen,
  Link2,
  Info,
  AtSign,
  Lock,
} from 'lucide-react';
import { Input, Radio, Tabs, Tag, Select, Switch } from 'antd';
import { UserSelect } from '@/components/common/UserSelect';

const { TextArea } = Input;

export default function TicketWorkbenchPrototypePage() {
  const [layoutMode, setLayoutMode] = useState<'stream' | 'split'>('stream');
  const [replyText, setReplyText] = useState('');
  const [isInternalComment, setIsInternalComment] = useState(false);
  const [mentionedUsers, setMentionedUsers] = useState<number[]>([]);

  // 模拟 #15 号工单数据（云服务器与权限申请）
  const mockTicket = {
    id: 15,
    ticketNumber: 'TKT-20260824-0015',
    title: '申请生产环境 阿里云 ECS 计算资源 (4C8G) 及 MySQL 数据库账号',
    status: 'in_progress',
    statusText: '处理中',
    priority: 'high',
    priorityText: '高优先级',
    source: 'service_catalog',
    sourceText: '服务目录申请',
    requester: {
      name: '张建国 (D05578)',
      department: '核心业务研发二组',
      email: 'jianguo.zhang@enterprise.com',
      phone: '13800138000',
    },
    assignee: {
      name: '李维 (运维一线工程师)',
      department: '基础架构运维部',
    },
    createdAt: '2026-08-24 09:30:15',
    sla: {
      name: '生产级资源交付 SLA',
      responseDeadline: '2026-08-24 10:30:00',
      resolutionDeadline: '2026-08-24 17:30:00',
      remainingHours: 4.5,
      isBreached: false,
    },
    description: `因“618 大促促销引擎重构”项目需要，需在阿里云华东1 (杭州) 可用区申请 2 台标准规格的计算节点，用于承载促销规则计算微服务。
同时需要开通只读访问生产从库 (rds-prod-read-01) 的专有网络白名单和只读账号。

【业务影响】
若无法在今天 18:00 前完成资源开通，将影响下周一全链路压测计划。`,
    
    // 随单附件数据（收纳在底部 Tab 中）
    attachments: [
      {
        id: 1,
        name: '促销引擎拓扑与网络规划图.png',
        size: '1.8 MB',
        uploadedBy: '张建国',
        uploadedAt: '09:30:15',
        type: 'image',
        previewUrl: 'https://images.unsplash.com/photo-1558494949-ef010cbdcc31?auto=format&fit=crop&w=400&q=80',
      },
      {
        id: 2,
        name: '阿里云ECS资源审批规格说明书_v2.pdf',
        size: '420 KB',
        uploadedBy: '张建国',
        uploadedAt: '09:30:18',
        type: 'file',
      },
    ],

    // 动态自定义字段 / 服务申请专属参数
    serviceRequest: {
      catalogName: '阿里云 ECS 云服务器申请',
      serviceType: '云基础设施',
      spec: 'ecs.c7.xlarge (4 vCPU / 8 GiB 内存 / 100GB ESSD)',
      quantity: 2,
      os: 'Alibaba Cloud Linux 3.2104 LTS 64位',
      costCenter: 'CC-TECH-2026-FIN02',
      dataClassification: '内部业务数据 (Internal)',
      needsPublicIp: '否 (仅 VPC 内部访问)',
      ipWhitelist: '172.16.10.0/24, 172.16.20.0/24',
      expireAt: '2027-08-24 (长期有效)',
      ciId: 1042,
      ciName: 'app-promotion-calc-cluster',
    },
    // 交付任务
    provisionTasks: [
      { id: 101, resourceType: '阿里云 ECS 实例', provider: 'Alicloud', status: 'succeeded', updatedAt: '10:05:22' },
      { id: 102, resourceType: 'VPC 安全组与白名单', provider: 'Alicloud', status: 'running', updatedAt: '10:12:00' },
    ],

    // 流程流转节点
    workflowSteps: [
      { key: 'created', label: '提单申请', status: 'finish', time: '09:30' },
      { key: 'approval', label: '主管审批 (王强)', status: 'finish', time: '09:38' },
      { key: 'delivery', label: '资源自动化开通', status: 'process', time: '进行中' },
      { key: 'closed', label: '验收归档', status: 'wait', time: '待处理' },
    ],

    // 知识库推荐
    kbArticles: [
      { id: 101, title: '《华东1杭州区 VPC 安全组配置规范与开放准则》', reads: 284 },
      { id: 102, title: '《生产环境 RDS MySQL 只读账号自助申请与审计要求》', reads: 512 },
    ],
  };

  const handleApplyAiSuggestion = (text: string) => {
    setReplyText(text);
  };

  // 底部多维 Tabs（完全对齐原始 CommentPanel + UserSelect 实现）
  const tabItems = [
    {
      key: 'comments',
      label: (
        <span className="flex items-center gap-1.5 text-xs font-medium">
          <MessageSquare size={13} />
          协作沟通与评论 (3)
        </span>
      ),
      children: (
        <div className="space-y-4 pt-2">
          {/* 消息时间线 */}
          <div className="space-y-3.5">
            {/* 留言 1: 申请人提单 */}
            <div className="flex items-start gap-3 text-xs">
              <div className="w-8 h-8 rounded-full bg-slate-100 text-slate-700 font-bold flex items-center justify-center shrink-0">
                张
              </div>
              <div className="flex-1 bg-slate-50 rounded-xl p-3.5 border border-slate-100 space-y-1">
                <div className="flex items-center justify-between">
                  <span className="font-semibold text-slate-900 text-xs">张建国 (D05578)</span>
                  <span className="text-slate-400 font-mono text-[11px]">09:30</span>
                </div>
                <p className="text-slate-700 m-0 leading-relaxed text-xs">已提交申请，请运维同学优先处理华东1可用区配置。</p>
              </div>
            </div>

            {/* 留言 2: 评估与协同留言 (带 @提及 和 内部可见标识) */}
            <div className="flex items-start gap-3 text-xs">
              <div className="w-8 h-8 rounded-full bg-amber-100 text-amber-800 font-bold flex items-center justify-center shrink-0">
                李
              </div>
              <div className="flex-1 bg-amber-50/50 rounded-xl p-3.5 border border-amber-200/70 space-y-1.5">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-1.5">
                    <span className="font-semibold text-slate-900 text-xs">李维 (运维一线)</span>
                    <span className="text-[10px] bg-amber-100 text-amber-800 px-1.5 py-0.2 rounded font-medium flex items-center gap-0.5">
                      <Lock size={10} /> 仅内部可见
                    </span>
                    <span className="text-[10px] bg-blue-100 text-blue-700 px-1.5 py-0.2 rounded font-medium flex items-center gap-0.5">
                      <AtSign size={10} /> @提及 1人
                    </span>
                  </div>
                  <span className="text-slate-400 font-mono text-[11px]">09:40</span>
                </div>
                <p className="text-slate-700 m-0 leading-relaxed text-xs">
                  已初步评估 4C8G 规格，目前华东1安全组规则需架构师核实是否允许只读连生产 RDS。
                </p>
              </div>
            </div>

            {/* 留言 3: 处理人常规回复 */}
            <div className="flex items-start gap-3 text-xs">
              <div className="w-8 h-8 rounded-full bg-orange-100 text-orange-700 font-bold flex items-center justify-center shrink-0">
                李
              </div>
              <div className="flex-1 bg-orange-50/30 rounded-xl p-3.5 border border-orange-100 space-y-1">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-1.5">
                    <span className="font-semibold text-slate-900 text-xs">李维 (运维一线)</span>
                    <span className="text-[10px] bg-orange-100 text-orange-700 px-1.5 py-0.2 rounded font-medium">处理人</span>
                  </div>
                  <span className="text-slate-400 font-mono text-[11px]">09:45</span>
                </div>
                <p className="text-slate-700 m-0 leading-relaxed text-xs">收到，计算规格与安全组规则正在自动化跑脚本开通，稍后同步结果。</p>
              </div>
            </div>
          </div>

          {/* 添加评论区域（严格对齐原 CommentPanel.tsx 实现架构） */}
          <div className="pt-3 mt-4 border-t border-slate-100 space-y-3">
            {/* 1. 仅内部可见勾选 */}
            <div className="flex items-center space-x-2">
              <input
                type="checkbox"
                id="internal-prototype-toggle"
                checked={isInternalComment}
                onChange={e => setIsInternalComment(e.target.checked)}
                className="rounded text-orange-600 focus:ring-orange-500 cursor-pointer"
              />
              <label htmlFor="internal-prototype-toggle" className="text-xs text-slate-600 font-medium cursor-pointer">
                仅内部可见
              </label>
            </div>

            {/* 2. 原版标准的 UserSelect 组件（支持真实用户搜索与头像选择） */}
            <div>
              <div className="mb-1.5">
                <span className="text-xs text-slate-500 font-medium flex items-center gap-1">
                  <AtSign size={13} className="text-slate-400" />
                  @用户（可选）
                </span>
              </div>
              <UserSelect
                value={mentionedUsers}
                onChange={setMentionedUsers}
                mode="multiple"
                placeholder="选择要@的用户"
                style={{ width: '100%' }}
              />
            </div>

            {/* 3. 评论输入框 */}
            <TextArea
              rows={4}
              placeholder="输入您的评论或内部评估记录..."
              value={replyText}
              onChange={e => setReplyText(e.target.value)}
              className="!rounded-xl !border-slate-200 !text-xs !p-3 shadow-none focus:!border-orange-500"
            />

            {/* 4. 发送按钮 */}
            <div className="flex justify-end pt-1">
              <button
                type="button"
                className="inline-flex items-center gap-1.5 px-4 py-1.5 rounded-lg text-xs font-medium bg-orange-500 hover:bg-orange-600 active:bg-orange-700 text-white transition-colors duration-150 cursor-pointer shadow-xs disabled:opacity-50"
                disabled={!replyText.trim()}
              >
                <Send size={13} />
                <span>发送评论</span>
              </button>
            </div>
          </div>
        </div>
      ),
    },
    {
      key: 'attachments',
      label: (
        <span className="flex items-center gap-1.5 text-xs font-medium">
          <Paperclip size={13} />
          附件 ({mockTicket.attachments.length})
        </span>
      ),
      children: (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 pt-2">
          {mockTicket.attachments.map(att => (
            <div
              key={att.id}
              className="flex items-center gap-3 p-3 bg-slate-50 hover:bg-slate-100/80 rounded-xl border border-slate-200/70 transition-all group cursor-pointer"
            >
              {att.type === 'image' && att.previewUrl ? (
                <div className="w-12 h-12 rounded-lg bg-slate-200 overflow-hidden shrink-0 border border-slate-200">
                  <img src={att.previewUrl} alt={att.name} className="w-full h-full object-cover" />
                </div>
              ) : (
                <div className="w-12 h-12 rounded-lg bg-slate-100 text-slate-600 flex items-center justify-center shrink-0 border border-slate-200">
                  <FileText size={22} />
                </div>
              )}
              <div className="min-w-0 flex-1 space-y-0.5">
                <p className="text-xs font-medium text-slate-800 truncate m-0 group-hover:text-orange-600 transition-colors">
                  {att.name}
                </p>
                <span className="text-[11px] text-slate-400 font-mono block">
                  {att.size} ｜ 上传者: {att.uploadedBy}
                </span>
              </div>
              <div className="flex items-center gap-1 shrink-0">
                <button type="button" className="w-7 h-7 rounded-lg bg-white text-slate-500 hover:text-orange-600 flex items-center justify-center border border-slate-200 shadow-2xs">
                  <Download size={12} />
                </button>
              </div>
            </div>
          ))}
        </div>
      ),
    },
    {
      key: 'approvals',
      label: (
        <span className="flex items-center gap-1.5 text-xs font-medium">
          <GitBranch size={13} />
          审批链 (1/1)
        </span>
      ),
      children: (
        <div className="space-y-3 pt-2 text-xs">
          <div className="p-3.5 bg-slate-50 rounded-xl border border-slate-100 space-y-2">
            <div className="flex items-center justify-between">
              <span className="font-bold text-slate-800">服务目录审批流 (BPMN 节点)</span>
              <span className="text-[11px] text-emerald-600 bg-emerald-50 border border-emerald-200 px-2 py-0.5 rounded font-medium">
                节点已通过
              </span>
            </div>
            <div className="text-slate-600 space-y-1 text-xs">
              <div className="flex justify-between">
                <span>审批人: 王强 (部门主管)</span>
                <span className="font-mono text-slate-400">2026-08-24 09:38:20</span>
              </div>
              <div className="bg-white p-2.5 rounded-lg border border-slate-100 text-slate-700">
                审批意见：同意申请，符合大促计算资源预算规划。
              </div>
            </div>
          </div>
        </div>
      ),
    },
    {
      key: 'history',
      label: (
        <span className="flex items-center gap-1.5 text-xs font-medium">
          <HistoryIcon size={13} />
          历史流转 (6)
        </span>
      ),
      children: (
        <div className="space-y-2.5 pt-2 text-xs">
          <div className="p-3 bg-slate-50 rounded-xl border border-slate-100 flex items-center justify-between">
            <div className="space-y-0.5">
              <span className="font-semibold text-slate-800">李维 将状态变更为「处理中」</span>
              <p className="text-[11px] text-slate-400 m-0">旧值: 已分配 → 新值: 处理中</p>
            </div>
            <span className="text-[11px] text-slate-400 font-mono">09:45:10</span>
          </div>
          <div className="p-3 bg-slate-50 rounded-xl border border-slate-100 flex items-center justify-between">
            <div className="space-y-0.5">
              <span className="font-semibold text-slate-800">系统自动分派给「李维 (运维一线)」</span>
              <p className="text-[11px] text-slate-400 m-0">路由策略: 阿里云基础设施组轮询规则</p>
            </div>
            <span className="text-[11px] text-slate-400 font-mono">09:38:25</span>
          </div>
          <div className="p-3 bg-slate-50 rounded-xl border border-slate-100 flex items-center justify-between">
            <div className="space-y-0.5">
              <span className="font-semibold text-slate-800">王强 审批通过该工单</span>
              <p className="text-[11px] text-slate-400 m-0">审批节点: 主管预算初审</p>
            </div>
            <span className="text-[11px] text-slate-400 font-mono">09:38:20</span>
          </div>
        </div>
      ),
    },
    {
      key: 'relations',
      label: (
        <span className="flex items-center gap-1.5 text-xs font-medium">
          <Link2 size={13} />
          关联工单与资产
        </span>
      ),
      children: (
        <div className="p-4 bg-slate-50 rounded-xl border border-slate-100 text-xs space-y-2 pt-2">
          <div className="flex items-center justify-between font-medium text-slate-700">
            <span>关联事件: INC-20260820-0042 (大促预演负载告警)</span>
            <Tag color="orange">已解决</Tag>
          </div>
          <p className="text-[11px] text-slate-500 m-0">本次云主机扩容系由上述压测事件引发的资源调优任务。</p>
        </div>
      ),
    },
  ];

  return (
    <div className="w-full space-y-4 text-slate-800 font-sans antialiased">
      {/* ================= 顶部原型控制条 ================= */}
      <div className="bg-slate-900 text-white px-4 py-2.5 rounded-xl shadow-sm flex flex-wrap items-center justify-between gap-3 text-xs">
        <div className="flex items-center gap-2">
          <span className="bg-orange-500 text-white font-semibold px-2 py-0.5 rounded text-[11px]">原型演示 v9</span>
          <span className="font-medium text-slate-200">
            已接入原始原版 UserSelect 组件（支持真实用户接口搜索、头像渲染与精准 @ 提及）
          </span>
        </div>

        <div className="flex items-center gap-3">
          <span className="text-slate-400">布局模式:</span>
          <Radio.Group
            value={layoutMode}
            onChange={e => setLayoutMode(e.target.value)}
            size="small"
            className="bg-slate-800 p-0.5 rounded-lg border border-slate-700"
          >
            <Radio.Button value="stream" className="!text-xs !bg-transparent text-slate-300">
              方案 A: 沉浸流式
            </Radio.Button>
            <Radio.Button value="split" className="!text-xs !bg-transparent text-slate-300">
              方案 B: 左右双屏分流
            </Radio.Button>
          </Radio.Group>
        </div>
      </div>

      {/* ================= 工单主 Header & 规范动作栏 ================= */}
      <div className="w-full bg-white rounded-2xl border border-slate-200/90 p-4 sm:p-5 shadow-xs">
        <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-4">
          {/* 左侧：返回、单号、标题、Tag */}
          <div className="space-y-2 min-w-0">
            <div className="flex items-center gap-2 text-xs text-slate-500">
              <Link href="/tickets" className="inline-flex items-center gap-1 text-slate-500 hover:text-slate-900 font-medium transition-colors">
                <ArrowLeft size={13} />
                返回工单列表
              </Link>
              <span>/</span>
              <span className="font-mono text-slate-400">{mockTicket.ticketNumber}</span>
              <span>/</span>
              <span className="text-slate-600">{mockTicket.sourceText}</span>
            </div>

            <div className="flex flex-wrap items-center gap-2.5">
              <h1 className="text-lg sm:text-xl font-bold text-slate-900 tracking-tight m-0 truncate">
                #{mockTicket.id} {mockTicket.title}
              </h1>
              <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-semibold bg-orange-50 text-orange-700 border border-orange-200">
                <span className="w-1.5 h-1.5 rounded-full bg-orange-500 mr-1.5" />
                {mockTicket.statusText}
              </span>
              <span className="inline-flex items-center px-2 py-0.5 rounded-md text-xs font-medium bg-slate-100 text-slate-700 border border-slate-200">
                {mockTicket.priorityText}
              </span>
            </div>
          </div>

          {/* 右侧：统一规范按钮系 */}
          <div className="flex flex-wrap items-center gap-2 self-start lg:self-center shrink-0">
            <button
              type="button"
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-orange-500 hover:bg-orange-600 active:bg-orange-700 text-white transition-colors duration-150 cursor-pointer shadow-xs"
            >
              <CheckCircle2 size={13} />
              <span>批准</span>
            </button>

            <button
              type="button"
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white hover:bg-slate-50 text-slate-700 border border-slate-200 hover:border-slate-300 transition-colors duration-150 cursor-pointer shadow-2xs"
            >
              <XCircle size={13} className="text-slate-500" />
              <span>拒绝</span>
            </button>

            <button
              type="button"
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white hover:bg-slate-50 text-slate-700 border border-slate-200 hover:border-slate-300 transition-colors duration-150 cursor-pointer shadow-2xs"
            >
              <UserCheck size={13} className="text-slate-500" />
              <span>转派分配</span>
            </button>

            <button
              type="button"
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white hover:bg-slate-50 text-slate-700 border border-slate-200 hover:border-slate-300 transition-colors duration-150 cursor-pointer shadow-2xs"
            >
              <Edit size={13} className="text-slate-500" />
              <span>编辑</span>
            </button>

            <button
              type="button"
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-white hover:bg-slate-50 text-slate-700 border border-slate-200 hover:border-slate-300 transition-colors duration-150 cursor-pointer shadow-2xs"
            >
              <Users size={13} className="text-slate-500" />
              <span>抄送</span>
            </button>
          </div>
        </div>
      </div>

      {/* ================= 方案 A 视图: 沉浸流式 ================= */}
      {layoutMode === 'stream' && (
        <div className="w-full grid grid-cols-1 lg:grid-cols-12 gap-5 items-start">
          {/* 左侧 8 列: 诉求内容 -> 服务规格 -> 底部完整 Tabs 协作流 */}
          <div className="lg:col-span-8 space-y-5 min-w-0">
            {/* 1. 核心诉求描述卡片 */}
            <div className="bg-white rounded-2xl border border-slate-200/90 p-5 shadow-xs space-y-4">
              <div className="flex items-center justify-between border-b border-slate-100 pb-3">
                <div className="flex items-center gap-2">
                  <span className="font-bold text-sm text-slate-800">工单诉求与业务描述</span>
                  <span className="text-[11px] text-slate-400">申请人填写</span>
                </div>
                <span className="text-xs font-mono text-slate-400">提交于 {mockTicket.createdAt}</span>
              </div>

              <div className="text-xs text-slate-700 leading-relaxed whitespace-pre-line bg-slate-50/70 p-4 rounded-xl border border-slate-100">
                {mockTicket.description}
              </div>
            </div>

            {/* 2. 结构化服务规格与履约面板 */}
            <div className="bg-white rounded-2xl border border-slate-200/90 p-5 shadow-xs space-y-4">
              <div className="flex items-center justify-between border-b border-slate-100 pb-3">
                <div className="flex items-center gap-2">
                  <div className="w-6 h-6 rounded-md bg-orange-50 text-orange-600 flex items-center justify-center font-bold text-xs">
                    ☁️
                  </div>
                  <span className="font-bold text-sm text-slate-800">服务申请与规格参数</span>
                  <span className="text-xs text-orange-700 bg-orange-50 px-2 py-0.5 rounded font-medium border border-orange-200">
                    {mockTicket.serviceRequest.catalogName}
                  </span>
                </div>

                <button
                  type="button"
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-orange-500 hover:bg-orange-600 active:bg-orange-700 text-white transition-colors duration-150 cursor-pointer shadow-xs"
                >
                  <PlayCircle size={13} />
                  <span>开始交付</span>
                </button>
              </div>

              {/* 规格字段网格 */}
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 text-xs">
                <div className="p-3 bg-slate-50 rounded-xl border border-slate-100 space-y-1">
                  <span className="text-slate-400 block text-[11px]">申请计算规格</span>
                  <span className="font-semibold text-slate-800 block font-mono text-xs">{mockTicket.serviceRequest.spec}</span>
                </div>

                <div className="p-3 bg-slate-50 rounded-xl border border-slate-100 space-y-1">
                  <span className="text-slate-400 block text-[11px]">操作系统与镜像</span>
                  <span className="font-semibold text-slate-800 block text-xs">{mockTicket.serviceRequest.os}</span>
                </div>

                <div className="p-3 bg-slate-50 rounded-xl border border-slate-100 space-y-1">
                  <span className="text-slate-400 block text-[11px]">成本中心 / 费用归属</span>
                  <span className="font-semibold text-slate-800 block font-mono text-xs">{mockTicket.serviceRequest.costCenter}</span>
                </div>

                <div className="p-3 bg-slate-50 rounded-xl border border-slate-100 space-y-1">
                  <span className="text-slate-400 block text-[11px]">数据安全等级</span>
                  <span className="font-semibold text-slate-800 block text-xs">{mockTicket.serviceRequest.dataClassification}</span>
                </div>

                <div className="p-3 bg-slate-50 rounded-xl border border-slate-100 space-y-1">
                  <span className="text-slate-400 block text-[11px]">公网访问需求</span>
                  <span className="font-semibold text-slate-800 block text-xs">{mockTicket.serviceRequest.needsPublicIp}</span>
                </div>

                <div className="p-3 bg-slate-50 rounded-xl border border-slate-100 space-y-1">
                  <span className="text-slate-400 block text-[11px]">源 IP 白名单</span>
                  <span className="font-semibold text-slate-800 block font-mono text-xs">{mockTicket.serviceRequest.ipWhitelist}</span>
                </div>
              </div>

              {/* 关联交付任务条目 */}
              <div className="pt-2">
                <span className="text-xs font-bold text-slate-700 mb-2 block">资源交付任务 (2)</span>
                <div className="space-y-2">
                  {mockTicket.provisionTasks.map(task => (
                    <div key={task.id} className="flex items-center justify-between p-2.5 bg-slate-50/90 rounded-lg border border-slate-100 text-xs">
                      <div className="flex items-center gap-2">
                        <span className="font-mono text-slate-400 text-xs">#{task.id}</span>
                        <span className="font-medium text-slate-700 text-xs">{task.resourceType}</span>
                        <span className="text-[11px] text-slate-400">({task.provider})</span>
                      </div>
                      <div className="flex items-center gap-3">
                        <span className="text-[11px] text-slate-400 font-mono">{task.updatedAt}</span>
                        {task.status === 'succeeded' ? (
                          <span className="text-[11px] text-slate-600 bg-slate-100 border border-slate-200 px-2 py-0.5 rounded font-medium">已完成</span>
                        ) : (
                          <span className="text-[11px] text-orange-600 bg-orange-50 border border-orange-200 px-2 py-0.5 rounded font-medium flex items-center gap-1">
                            <span className="w-1.5 h-1.5 rounded-full bg-orange-500 animate-ping" /> 执行中
                          </span>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>

            {/* 3. 底部完整协作、审批与历史 Tabs */}
            <div className="bg-white rounded-2xl border border-slate-200/90 p-5 shadow-xs space-y-3">
              <div className="flex items-center gap-2 text-slate-500 text-xs font-semibold border-b border-slate-100 pb-2">
                <Info size={13} />
                协作流、审批链与审计历史
              </div>

              <Tabs items={tabItems} defaultActiveKey="comments" className="custom-ticket-tabs" />
            </div>
          </div>

          {/* 右侧 4 列: 【高密度运维工具箱 + 悬浮跟随】 */}
          <div className="lg:col-span-4 space-y-4 sticky top-4 min-w-0">
            {/* 1. 工单属性与上下文 (置顶) */}
            <div className="bg-white rounded-2xl border border-slate-200/90 p-4 shadow-xs space-y-3 text-xs">
              <span className="font-bold text-slate-800 block border-b border-slate-100 pb-2 text-xs">
                工单上下文属性
              </span>

              <div className="space-y-2.5">
                <div className="flex items-center justify-between">
                  <span className="text-slate-400 text-xs">申请人:</span>
                  <span className="font-medium text-slate-800 text-xs">{mockTicket.requester.name}</span>
                </div>

                <div className="flex items-center justify-between">
                  <span className="text-slate-400 text-xs">所属部门:</span>
                  <span className="text-slate-700 text-xs">{mockTicket.requester.department}</span>
                </div>

                <div className="flex items-center justify-between">
                  <span className="text-slate-400 text-xs">当前处理人:</span>
                  <span className="font-semibold text-orange-800 bg-orange-50 px-2 py-0.5 rounded border border-orange-200 text-xs">
                    {mockTicket.assignee.name}
                  </span>
                </div>

                <div className="flex items-center justify-between">
                  <span className="text-slate-400 text-xs">工单分类:</span>
                  <span className="text-slate-700 text-xs">云资源与基础设施</span>
                </div>
              </div>
            </div>

            {/* 2. ⚡ AI 智能辅助建议卡片 */}
            <div className="bg-gradient-to-br from-orange-50/60 to-amber-50/40 rounded-2xl border border-orange-200/70 p-4 shadow-xs space-y-2.5 text-xs">
              <div className="flex items-center justify-between border-b border-orange-200/50 pb-2">
                <span className="font-bold text-slate-800 flex items-center gap-1.5 text-xs text-orange-900">
                  <Sparkles size={13} className="text-orange-500" />
                  AI 处置建议 & 快速草稿
                </span>
                <span className="text-[10px] text-orange-600 bg-orange-100/70 px-1.5 py-0.2 rounded font-medium">
                  自动分析
                </span>
              </div>

              <p className="text-[11px] text-slate-600 m-0 leading-relaxed">
                💡 历史相似单：<span className="font-mono text-slate-800">#8 (上周五)</span> 同类规格在杭州可用区通过 Ansible 脚本开通耗时 12 分钟。
              </p>

              <div className="pt-1 flex items-center justify-between">
                <span className="text-[11px] text-slate-500">一键填入回复草稿:</span>
                <button
                  type="button"
                  onClick={() => handleApplyAiSuggestion('已核实华东1可用区资源配额充足，安全组与RDS只读白名单正在自动下发中，预计10分钟内开通完毕。')}
                  className="text-[11px] text-orange-600 hover:text-orange-700 font-medium hover:underline cursor-pointer bg-white px-2 py-0.5 rounded border border-orange-200 shadow-2xs"
                >
                  填入草稿 ↵
                </button>
              </div>
            </div>

            {/* 3. 📊 流转节点时间轴 */}
            <div className="bg-white rounded-2xl border border-slate-200/90 p-4 shadow-xs space-y-3 text-xs">
              <span className="font-bold text-slate-800 block border-b border-slate-100 pb-2 text-xs">
                流转节点进度 (BPMN)
              </span>

              <div className="space-y-2.5">
                {mockTicket.workflowSteps.map((step, idx) => (
                  <div key={step.key} className="flex items-center justify-between text-xs">
                    <div className="flex items-center gap-2">
                      {step.status === 'finish' ? (
                        <span className="w-4 h-4 rounded-full bg-emerald-100 text-emerald-600 flex items-center justify-center text-[10px] font-bold">
                          ✓
                        </span>
                      ) : step.status === 'process' ? (
                        <span className="w-4 h-4 rounded-full bg-orange-100 text-orange-600 flex items-center justify-center text-[10px] font-bold animate-pulse">
                          ●
                        </span>
                      ) : (
                        <span className="w-4 h-4 rounded-full bg-slate-100 text-slate-400 flex items-center justify-center text-[10px]">
                          ○
                        </span>
                      )}
                      <span className={`text-xs ${step.status === 'process' ? 'font-bold text-orange-700' : 'text-slate-700'}`}>
                        {step.label}
                      </span>
                    </div>
                    <span className="text-[11px] text-slate-400 font-mono">{step.time}</span>
                  </div>
                ))}
              </div>
            </div>

            {/* 4. SLA 时限监控卡片 */}
            <div className="bg-white rounded-2xl border border-slate-200/90 p-4 shadow-xs space-y-3">
              <div className="flex items-center justify-between">
                <span className="text-xs font-bold text-slate-800 flex items-center gap-1.5">
                  <Clock size={14} className="text-slate-500" />
                  SLA 时效与承诺
                </span>
                <span className="text-[11px] text-slate-700 bg-slate-100 border border-slate-200 px-2 py-0.5 rounded font-medium">
                  正常流转中
                </span>
              </div>

              <div className="bg-slate-50 p-3 rounded-xl border border-slate-100 space-y-2 text-xs">
                <div className="flex items-center justify-between">
                  <span className="text-slate-500 text-[11px]">解决截止时限:</span>
                  <span className="font-mono font-bold text-slate-800 text-xs">今天 17:30</span>
                </div>
                <div className="w-full bg-slate-200 h-1.5 rounded-full overflow-hidden">
                  <div className="bg-orange-500 h-full w-[45%]" />
                </div>
                <div className="flex justify-between text-[11px] text-slate-400 font-mono">
                  <span>剩余 4.5 小时</span>
                  <span>目标 8.0 小时</span>
                </div>
              </div>
            </div>

            {/* 5. 关联 CMDB 配置项 (CI) */}
            <div className="bg-white rounded-2xl border border-slate-200/90 p-4 shadow-xs space-y-3 text-xs">
              <div className="flex items-center justify-between border-b border-slate-100 pb-2">
                <span className="font-bold text-slate-800 flex items-center gap-1.5 text-xs">
                  <Server size={14} className="text-slate-500" />
                  关联配置项 (CI)
                </span>
                <span className="text-[11px] text-slate-600 hover:text-orange-600 hover:underline cursor-pointer flex items-center gap-0.5">
                  拓扑图 <ExternalLink size={11} />
                </span>
              </div>

              <div className="p-3 bg-slate-50 rounded-xl border border-slate-100 space-y-1.5">
                <div className="flex items-center justify-between font-mono text-slate-800 font-bold text-xs">
                  <span>{mockTicket.serviceRequest.ciName}</span>
                  <span className="text-[10px] text-slate-600 bg-slate-200 px-1.5 py-0.2 rounded font-normal">应用集群</span>
                </div>
                <p className="text-[11px] text-slate-500 m-0">生产核心促销微服务集群，关联 14 个计算实例</p>
              </div>
            </div>

            {/* 6. 关联知识库规程推荐 (KB Articles) */}
            <div className="bg-white rounded-2xl border border-slate-200/90 p-4 shadow-xs space-y-2.5 text-xs">
              <div className="flex items-center justify-between border-b border-slate-100 pb-2">
                <span className="font-bold text-slate-800 flex items-center gap-1.5 text-xs">
                  <BookOpen size={13} className="text-slate-500" />
                  推荐操作指引 (KB)
                </span>
                <span className="text-[11px] text-slate-400">2 篇</span>
              </div>

              <div className="space-y-2">
                {mockTicket.kbArticles.map(kb => (
                  <div key={kb.id} className="p-2.5 bg-slate-50 hover:bg-orange-50/50 rounded-lg border border-slate-100 transition-colors cursor-pointer group">
                    <p className="text-[11px] font-medium text-slate-700 group-hover:text-orange-600 truncate m-0">
                      {kb.title}
                    </p>
                    <span className="text-[10px] text-slate-400 block mt-0.5">阅读量: {kb.reads} 次</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </div>
      )}

      {/* ================= 方案 B 视图: 左右等宽双屏对照 ================= */}
      {layoutMode === 'split' && (
        <div className="w-full grid grid-cols-1 lg:grid-cols-2 gap-5 items-start">
          <div className="space-y-4">
            <div className="bg-white rounded-2xl border border-slate-200/90 p-5 shadow-xs space-y-4">
              <span className="font-bold text-sm text-slate-900 block border-b border-slate-100 pb-2">
                1. 业务诉求与详细背景
              </span>
              <div className="text-xs text-slate-700 leading-relaxed whitespace-pre-line bg-slate-50 p-3.5 rounded-xl border border-slate-100">
                {mockTicket.description}
              </div>

              <span className="font-bold text-sm text-slate-900 block border-b border-slate-100 pt-2 pb-2">
                2. 结构化申请规格
              </span>
              <div className="grid grid-cols-2 gap-2 text-xs">
                <div className="p-2.5 bg-slate-50 rounded-lg border border-slate-100">
                  <span className="text-slate-400 block text-[10px]">规格:</span>
                  <span className="font-mono font-semibold text-slate-800">{mockTicket.serviceRequest.spec}</span>
                </div>
                <div className="p-2.5 bg-slate-50 rounded-lg border border-slate-100">
                  <span className="text-slate-400 block text-[10px]">成本中心:</span>
                  <span className="font-mono font-semibold text-slate-800">{mockTicket.serviceRequest.costCenter}</span>
                </div>
                <div className="p-2.5 bg-slate-50 rounded-lg border border-slate-100">
                  <span className="text-slate-400 block text-[10px]">数据分级:</span>
                  <span className="font-semibold text-slate-800">{mockTicket.serviceRequest.dataClassification}</span>
                </div>
                <div className="p-2.5 bg-slate-50 rounded-lg border border-slate-100">
                  <span className="text-slate-400 block text-[10px]">数量:</span>
                  <span className="font-mono font-semibold text-slate-800">{mockTicket.serviceRequest.quantity} 台</span>
                </div>
              </div>

              <span className="font-bold text-sm text-slate-900 block border-b border-slate-100 pt-2 pb-2">
                3. 关联 CMDB 资产
              </span>
              <div className="p-3 bg-slate-50 rounded-xl border border-slate-100 text-xs">
                <span className="font-bold text-slate-800 font-mono">{mockTicket.serviceRequest.ciName}</span>
                <span className="text-slate-500 block text-[11px] mt-0.5">关联 14 个下游生产节点</span>
              </div>
            </div>
          </div>

          <div className="space-y-4">
            <div className="bg-white rounded-2xl border border-slate-200/90 p-5 shadow-xs space-y-4">
              <div className="flex items-center justify-between border-b border-slate-100 pb-3">
                <span className="font-bold text-sm text-slate-900">协同流与处置记录</span>
                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    className="px-2.5 py-1 rounded-md text-xs font-medium bg-orange-500 hover:bg-orange-600 text-white transition-colors"
                  >
                    批准
                  </button>
                  <button
                    type="button"
                    className="px-2.5 py-1 rounded-md text-xs font-medium bg-white hover:bg-slate-50 text-slate-700 border border-slate-200 transition-colors"
                  >
                    拒绝
                  </button>
                </div>
              </div>

              <div className="space-y-3 text-xs max-h-[380px] overflow-y-auto pr-1">
                <div className="p-3 bg-slate-50 rounded-xl border border-slate-100 space-y-1">
                  <div className="flex justify-between font-semibold text-slate-800">
                    <span>张建国</span>
                    <span className="text-slate-400 font-mono text-[10px]">09:30</span>
                  </div>
                  <p className="m-0 text-slate-600">已提交申请，请尽快交付。</p>
                </div>

                <div className="p-3 bg-orange-50/30 rounded-xl border border-orange-100 space-y-1">
                  <div className="flex justify-between font-semibold text-slate-800">
                    <span>李维 (处理人)</span>
                    <span className="text-slate-400 font-mono text-[10px]">09:45</span>
                  </div>
                  <p className="m-0 text-slate-600">正在自动化拉起实例。</p>
                </div>
              </div>

              <div className="pt-3 mt-4 border-t border-slate-100 space-y-2">
                <TextArea rows={3} placeholder="快速写下处理意见或与客户沟通..." className="!rounded-xl text-xs" />
                <button
                  type="button"
                  className="w-full py-1.5 rounded-lg text-xs font-medium bg-orange-500 hover:bg-orange-600 text-white transition-colors cursor-pointer"
                >
                  发送并更新
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}


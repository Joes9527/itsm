# 工单详情处置工作台（Ticket Detail Workbench）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `TicketDetail.tsx` 页面重构为现代专业的高密度运维处置工作台（左侧主信息流 8 列 + 右侧悬浮运维工具箱 4 列，全宽自适应），并确保原有所有业务逻辑、弹窗与权限动作 100% 零丢失。

**Architecture:** 
- 保留现有的所有 React State、Handlers（`handleApprove`、`handleAssignSubmit`、`handleEditSubmit` 等）与 API 调用；
- 重塑外层排版为 `w-full grid grid-cols-1 lg:grid-cols-12 gap-5` 响应式栅格；
- 顶部集成 Header 动作控制台（继承 `ticket.actions` 门控与暖橙主色体系）；
- 右侧搭建 Sticky 悬浮运维工具箱（上下文属性、AI Copilot、流转节点进度、SLA 仪表与关联 CI）；
- 底部无缝集成 5 维标准 Tabs（`CommentPanel` 含 `UserSelect` @提及与仅内部笔记、`AttachmentPanel`、`ApprovalWorkflowPanel`、`HistoryTimeline`、`RelationPanel`）。

**Tech Stack:** Next.js (App Router), React, TypeScript, Ant Design, Tailwind CSS, Lucide React.

**Spec:** `docs/superpowers/specs/2026-08-24-ticket-detail-workbench-redesign.md`

## Global Constraints
- 前端所有 API 交互遵循 camelCase 规范；
- 必须使用 `common.Success` / 规范 DTO 映射，严禁破坏后端已联调契约；
- 按钮规范：主操作（如批准）采用 `bg-orange-500 hover:bg-orange-600`，次级操作采用白底幽灵线框（Ghost Button）；
- 严禁删除现有任何弹窗（Assign/Edit/CC/Delete）与动态自定义字段支持；
- 任何修改必须通过 `cd itsm-frontend && npm run type-check` 与 Jest 测试套件。

---

### Task 1: 重构 `TicketDetail.tsx` 核心工作台架构与 Header 动作控制台

**Files:**
- Modify: `itsm-frontend/src/components/ticket/TicketDetail.tsx`
- Test: `itsm-frontend/src/components/ticket/__tests__/TicketDetail.test.tsx` (or package check)

**Interfaces:**
- Consumes: `ticket.actions` ({ approve, reject, assign, edit, cc, delete })
- Produces: 现代 Header 动作控制台、全宽响应式容器、保留所有受控 Modals

- [ ] **Step 1: 重构 `TicketDetail.tsx` 的 JSX 骨架为 2-Column Grid**
- [ ] **Step 2: 接入 Header 动作栏，绑定现有 Handlers 与 `disabled` 状态**
- [ ] **Step 3: 挂载右侧运维上下文工具箱（属性置顶、AI 面板、SLA 进度条、CI 关联）**
- [ ] **Step 4: 挂载底部 5 维标准 Tabs 协同流（评论、附件、审批链、历史、关联）**
- [ ] **Step 5: 运行 TypeScript 编译检查确认无语法/类型错误**

Run: `cd /home/administrator/project/itsm/itsm-frontend && npm run type-check`
Expected: PASS (Exit code 0)

---

### Task 2: 验证弹窗、业务流与端到端功能完整性

**Files:**
- Test: `itsm-frontend/src/components/ticket/TicketDetail.tsx`
- Verify Route: `/tickets/15` 及其他工单详情

- [ ] **Step 1: 走查并验证分配弹窗（AssignModal + UserSelect）正常唤起并提交**
- [ ] **Step 2: 走查并验证编辑弹窗（EditModal + 动态自定义字段）正常保存**
- [ ] **Step 3: 走查并验证抄送弹窗（CCModal + 渠道多选）正常触发**
- [ ] **Step 4: 走查并验证删除弹窗（DeleteModal）二次防误删确认**
- [ ] **Step 5: 走查服务目录单（Service Request）的交付面板展示与流转**
- [ ] **Step 6: 走查底部评论区发表、@提及协同人与仅内部工作笔记切换**

---

### Task 3: 清理原型代码并完成全套测试回归

**Files:**
- Delete: `itsm-frontend/src/app/(main)/tickets/prototype/`

- [ ] **Step 1: 删除临时原型页面目录**
- [ ] **Step 2: 运行前端全量类型检查与 Jest 测试套件**
- [ ] **Step 3: Git 状态确认与代码提交**

Run: `cd /home/administrator/project/itsm/itsm-frontend && npm run type-check && npm test -- --testPathPattern=ticket`
Expected: PASS

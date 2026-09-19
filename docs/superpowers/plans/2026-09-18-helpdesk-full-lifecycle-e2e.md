# Helpdesk 完整生命周期闭环端到端验证实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 基于真实企业业务人员（Luka、赵颖、彭军、王金海），在自动化测试套件中补齐从“邮件进单 → 服务台工单初审与分派 → 处理人响应与状态流转 → 内部技术排查备注与申请人可见性隔离 → 公开沟通与方案解决 → 关单与历史审计”的 IT Helpdesk 服务台端到端完整闭环。

**Architecture:** 
1. 权限与角色就绪：确保服务台主管赵颖（`D33080`）具备 `sd_manager` 角色及 `ticket:assign`、`ticket:update` 权限，确保内部备注管理权（`canManageInternalComments`）生效。
2. 自动化套件升级：在 `tests/e2e/sslvpn-runbook-full.spec.ts` 中重构/升级 Phase 6，形成规范的 Helpdesk 运营全生命周期 7 步闭环用例。
3. 真实 UI 操作与严密断言：通过 Playwright 有头走查，验证转派弹窗、用户检索、分派提交、内部备注标签与非服务台端数据过滤、状态变更与解决方案必填、关单流转。
4. 运行手册与记录同步：更新 `docs/testing/email-ticket-ui-runbook.md` 和 `docs/testing/sslvpn-e2e-verification-record.md`。

**Tech Stack:** Go 1.22+, Gin, Ent ORM, Next.js 14, Playwright, PostgreSQL.

---

## 阶段规划与任务清单

### Task 1: 服务台主管角色与分派权限基线对齐 (Database & RBAC)

**Files:**
- Modify: `itsm-backend/pkg/seeder/seeder.go:1796-1803`
- Script: `docker exec itsm-postgres-dev psql ...`

**Interfaces & Data:**
- `sd_manager` 角色获得权限：`ticket:assign`, `ticket:resolve`, `ticket:close`
- 用户 `D33080`（赵颖）角色配置为 `sd_manager`

- [x] **Step 1: 检查并补充 `seeder.go` 中 `sd_manager` 缺失的工单生命周期权限**
  - 在 `sd_manager` 权限列表中增加 `"ticket:assign"`, `"ticket:resolve"`, `"ticket:close"`。
- [x] **Step 2: 对齐当前本地数据库中 `sd_manager` 角色权限与 `D33080` 用户角色**
  - 更新 `users` 表：`UPDATE users SET role = 'sd_manager' WHERE username = 'D33080';`
  - 为 `sd_manager` 角色在 `role_permissions` 中补齐 `ticket:assign`, `ticket:resolve`, `ticket:close` 权限关联。
- [x] **Step 3: 验证 API 与中间件鉴权**
  - 使用赵颖账号登录获取 Token，调用 `GET /api/v1/tickets/:id`，断言返回的 `actions.assign.allowed === true`。同时修复了 `authorization/workitem_scope.go` 与 `authorization/workitem_read_scope.go` 中 `sd_manager` 的行级数据可见性（`WorkItemDataScopeAllRole`），重新编译并部署后端服务。

---

### Task 2: 编写与完善 Phase 6 Helpdesk 完整生命周期自动化测试

**Files:**
- Modify: `itsm-frontend/tests/e2e/sslvpn-runbook-full.spec.ts`

**Lifecycle Steps 覆盖:**
1. **Step 6.1**: Luka（王雅蓉）通过 Microsoft Graph API 发送申报邮件至服务台。
2. **Step 6.2**: 等待轮询建单，自动生成工单（状态 `open`，未分派）。
3. **Step 6.3**: 申请人 Luka 登录核查工单生成与邮件来源属性。
4. **Step 6.4**: 服务台主管赵颖（`D33080`）登录 UI：
   - 打开工单详情，核对基础信息。
   - 点击【转派分配】，在弹窗中检索并选择二线网络工程师王金海（`D47105`），填写分派原因并提交。
   - 断言：工单处理人变更为王金海，工单时间线记录转派审计。
5. **Step 6.5**: 二线网络工程师王金海（`D47105`）接单与排查：
   - 登录系统打开工单详情。
   - 勾选【仅内部可见】复选框，发布内部排查备注（包含敏感网段规划与 `SEC-{TIMESTAMP}` 标识）。
   - 断言：评论区出现该条带有【仅内部可见】徽章的备注。
6. **Step 6.6**: 申请人 Luka（`D42784`）安全隔离验证：
   - 登录系统打开工单详情。
   - 断言：页面中**绝对不包含**王金海发布的内部排查备注正文与敏感标记（严格验证数据隔离安全性）。
7. **Step 6.7**: 工程师对外沟通与完成解决：
   - 王金海登录系统，未勾选内部可见，发布面向申请人的公开协同答复。
   - 点击【编辑】按钮，将状态变更为【已解决】（`resolved`），在必填的【解决方案】中输入真实解决方案，提交保存。
   - 断言：工单状态变为已解决，解决方案正确存档。
8. **Step 6.8**: 申请人确认与关单收尾：
   - Luka 登录系统，查看公开沟通与解决方案，发布确认消息：“【用户确认】经测试已可正常拨入内网，感谢处理，确认关闭。”
   - 服务台主管赵颖登录执行关单（点击【关闭工单】并二次确认），工单状态流转至 `closed`。

- [x] **Step 1: 在 `sslvpn-runbook-full.spec.ts` 中更新 Phase 6 用例代码**
- [x] **Step 2: 调试并验证每个步骤的选择器与异步等待机制**

---

### Task 3: 本地 Headed 可视化全流程实测与运行记录同步

**Files:**
- Modify: `docs/testing/email-ticket-ui-runbook.md`
- Modify: `docs/testing/sslvpn-e2e-verification-record.md`

- [x] **Step 1: 以 Headed 模式在本地运行 Phase 6 与 Phase 7 测试套件**
  - 执行 `DISPLAY=:0 npx playwright test tests/e2e/sslvpn-runbook-full.spec.ts -g "Phase [67]" --project=chromium --headed`，Windows 宿主机桌面 Chromium 窗口全流程可视化呈现。
- [x] **Step 2: 输出包含分诊订正、转派、内部备注隔离、解决方案、关单的真实测试执行证据**
  - Phase 6 (多部门协同审批流转): 单号 `TKT-202609-000082` (ID: #84)，Luka 提单 -> 赵颖初核 -> 彭军初审 -> 王金海复审 -> Luka 全流程留痕验收 PASS。
  - Phase 7 (服务台全生命周期闭环): 单号 `TKT-202609-000083` (ID: #85)，Luka 邮件提单 -> 赵颖分诊订正高优先级 -> 赵颖转派给王金海 -> 王金海内部排查笔记 -> Luka 数据安全隔离断言 -> 王金海公开答复并录入解决方案解决工单 -> Luka 确认回复 -> 赵颖正式关单归档 PASS。
  - 联合运行实测: `2 passed (2.9m)`，全流程 100% 绿色零缺陷通过。
- [x] **Step 3: 更新运行手册与执行记录文档**

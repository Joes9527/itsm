# 创建入口分类 ID 契约（P2）实施计划

> 状态：**DRAFT / 已转为 backlog（BL-CTI-02）/ 本次未实施**。本计划仅记录任务拆解与渠道证据；实施需先由维护者确认 §9.6 的待决问题。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 创建分类只接受最深节点 ID（`CTIInput` + 目录默认分类），删除四条创建投影里的 5 个按显示名称解析的槽位及其解析分支。

**Architecture:** 创建分类的唯一权威是 `TicketCategoryService.ResolveCreationClassification`（`itsm-backend/service/ticket_category_creation.go`），它由 WorkItem intake 在创建事务内调用。名称槽位是该函数内的第二套解析路径，删除后创建分类只有一个输入形态：节点 ID；外部若仍发名称，由既有的"拒绝未知字段"边界显式报错（失败关闭），而不是静默按名称匹配。

**Tech Stack:** Go 1.25 / Gin / Ent / testify；PostgreSQL 16（RLS）；Next.js/TypeScript（仅类型对齐）。

**Spec:** `docs/superpowers/specs/2026-09-17-cti-governance-design.md`（§5.1 创建权威、§8.C 同类兼容性决策）；`AGENTS.md` CTI governance contract（最深节点、L33 删除废弃路径、L53 兼容性决策、§7 DoR）。

## Global Constraints

- 分类只保存**最深节点 ID**；C/T/I 路径是派生投影，不得成为第二写入权威。
- 创建必须复用既有 intake 与领域创建器（`handlers/common/workitemcreation`），不得新增建单路径。
- 边界失败关闭：未知字段显式报错，不静默忽略（intake HTTP 与创建命令均已 `DisallowUnknownFields`）。
- 不保留过渡机制/别名/映射；回滚 = `git revert`。
- 共享环境：本任务**不写共享库、无迁移、无 seed、无部署**。
- 提交遵循 Conventional Commits；一个分支只做本目标；PR 描述含目标/影响/证据/风险/未验证项。

---

### Task 1: 创建名称槽位在边界被显式拒绝

**Files:**
- Test: `itsm-backend/handlers/common/workitemcreation/command_test.go`（或该包既有测试文件）
- Modify: `itsm-backend/handlers/common/workitemcreation/command.go:85,88,111-112,120`（删除 `Category`/`Subcategory` 字段）

**Interfaces:**
- Consumes: 现有命令解码函数（`DecodeCommand`/等价入口，`DisallowUnknownFields` 已启用，见 `command.go:343`）
- Produces: 4 个投影结构不再含名称分类字段

- [ ] **Step 1: 写失败测试**：断言以 `{"incident":{"category":"network","subcategory":"cpu"}}` 解码创建命令时报"未知字段"错误（`category`）。
- [ ] **Step 2: 运行并确认失败**：`cd itsm-backend && go test ./handlers/common/workitemcreation -run TestDecodeRejectsLegacyCategoryNames -count=1` → 期望失败（当前能解码成功）。
- [ ] **Step 3: 最小实现**：从 `IncidentInput`/`ProblemInput`/`ChangeInput`/`GenericInput` 删除 `Category`/`Subcategory` 字段。
- [ ] **Step 4: 确认通过**：同一命令 → PASS。
- [ ] **Step 5: 提交**：`refactor(intake): reject retired category name slots at the creation boundary`

### Task 2: 删除名称解析分支

**Files:**
- Modify: `itsm-backend/service/ticket_category_creation.go:33-61`（删除 `category`/`subcategory` 解析与"subcategory requires category"/"conflicts with CTI type"分支）
- Test: `itsm-backend/service/ticket_category_creation_test.go`（新增/更新）

**Interfaces:**
- Consumes: Task 1 后的命令结构（无名称槽位）
- Produces: `ResolveCreationClassification` 只读 `command.CTI` 与 `command.CatalogDefaultCategoryID`

- [ ] **Step 1: 写失败测试**：同一分类以 `CTI.CategoryID` 提供时解析出相同最深节点；`ResolvedCTI` 不再从名称派生。
- [ ] **Step 2: 运行确认失败**（若通过说明测试未覆盖新行为 → 重写）。
- [ ] **Step 3: 删除名称解析分支**，保留目录默认分类校验与"客户端重复同一最深节点可兼容、不同路径冲突"规则。
- [ ] **Step 4: 运行 `go test ./service -run 'TestResolveCreationClassification' -count=1` → PASS。**
- [ ] **Step 5: 提交**：`refactor(intake): resolve creation classification only from node ids`

### Task 3: 前端请求类型与后端一致（去掉名称槽位）

**Files:**
- Modify: `itsm-frontend/src/lib/api/incident-api.ts:67-68/164-165/228`、`itsm-frontend/src/lib/api/problem-api.ts:26/30/77`（**只改请求类型**；响应类型若仍由后端投影名称则保留）
- Test: 相邻 `__tests__/`（若存在创建请求契约用例）

**Interfaces:**
- Produces: 创建请求类型只含 `cti`/`categoryId`，不含 `category`/`subcategory`

- [ ] **Step 1: 写失败测试**：用 `@ts-expect-error` 固定"创建请求类型不接受 `category` 名称"。
- [ ] **Step 2: 验证守卫生效**（临时移除注解应由 `npm run type-check` 报错）。
- [ ] **Step 3: 删除请求类型里的名称字段**（区分请求/响应；响应类型不动）。
- [ ] **Step 4: `npm run type-check` + 相关 Jest** → 通过。
- [ ] **Step 5: 提交**：`refactor(api): drop the retired category name slots from creation requests`

### Task 4: 兼容性决策与渠道核实记录

**Files:**
- Modify: `docs/superpowers/specs/2026-09-17-cti-governance-design.md`（新增 §8.D：创建入口名称槽位退役决策、回滚边界、影响渠道）
- Modify: 本计划文档（追加执行记录：验证命令与结果；未验证项）

- [ ] **Step 1:** 记录决策：直接删除、无过渡；边界失败关闭；回滚=revert。
- [ ] **Step 2:** 记录渠道核实证据：KAF intake 边界要求"已解析的分类 ID"（`handlers/intake/snapshot_repository.go:128`）；邮件建单不设置分类（`service/ticket_email_creation.go` 无分类字段）；KAF 侧契约模型仅**声明**名称槽位而未构造使用（`~/apps/itsm-kaf/kaf-handoff-69bd0901/src/acp/contracts/workitem_intake.py:152-195`，非测试代码无构造点）。
- [ ] **Step 3:** `git diff --check`，提交 `docs(intake): record the creation name-slot retirement decision`。

### Task 5: 全量验证与交付

- [ ] **Step 1:** `cd itsm-backend && go build ./... && go test ./... -count=1`（记录包数与 FAIL 数）。
- [ ] **Step 2:** `go test ./tests/contract ./tests/rbac -count=1`。
- [ ] **Step 3:** 前端 `npm run type-check`；受影响的 Jest 套件。
- [ ] **Step 4:** `git diff --check`；确认无临时文件/构建产物入库。
- [ ] **Step 5:** 推送分支并开 PR（模板：目标/影响/验证证据/风险/未验证项），在 PR 中列出**跨仓库待办**：KAF 侧 `workitem_intake.py` 的名称槽位需在 KAF 仓库单独清理（其自身分支/PR）。

---

## 跨仓库协调（§9 合并顺序）

- ITSM 侧删除名称槽位后，KAF 若仍发名称会收到**显式错误**（失败关闭）；KAF 侧契约模型需在 **KAF 仓库**单独提交删除（其非测试代码当前未使用这些字段）。
- 建议顺序：KAF 契约清理与 ITSM 本 PR 可并行，但 **KAF 部署不得晚于 ITSM 部署**（KAF 未使用该字段，风险低）。

## Self-Review

- 覆盖：名称槽位删除（Task 1）、解析分支删除（Task 2）、前端类型对齐（Task 3）、兼容性决策与渠道证据（Task 4）、验证与交付（Task 5）✓
- 无占位符：每个 Task 有具体文件与命令 ✓
- 类型一致：Task 1 删除字段后 Task 2 编译才可能通过；Task 2 产出 `ResolvedCTI` 不变 ✓

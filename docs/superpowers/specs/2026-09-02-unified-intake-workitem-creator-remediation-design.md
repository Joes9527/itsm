# Unified Intake × P1 归并与 WorkItem 创建收口总设计

> 状态：Approved for planning（Phase 1）
> 日期：2026-09-02
> 当前代码基线：`origin/main` = `ca29f626`（P1 Wave：NumberAllocator、BPMN 残留授权、callback 效果语义、工单审批单轨化 + legacy Workflow 引擎删除均已合并）
> 上位/并行设计：`2026-08-30-architecture-assessment-remediation-execution-plan-design.md`（W0）、`2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md`（KAF 委派与统一受理的原始设计）、`2026-08-31-unified-intake-design.md`（Unified Intake 详细设计，实施于 `feat/kaf-delegation-transactional-delivery`，未合并）
> 依据：AGENTS.md、上述三份设计文档、以及本次对 `feat/kaf-delegation-transactional-delivery` 分支代码与 SQL 迁移内容的逐行核对（不采信任何文档自身的完成状态声明）

## 一、目的与定位

本文档解决一个已确认的规划归并问题：`2026-08-30-architecture-assessment-remediation-execution-plan-design.md` 的 **W0**（WorkItemCreator + SLA policy binder + 专业域物理收口）与 `2026-08-28` 起的 KAF 委派设计线所产出的 **Unified Intake**（`handlers/intake`，已在 `feat/kaf-delegation-transactional-delivery` 分支实施并 Dev 验证），是同一个技术问题的两次独立设计——两条线彼此从未互相引用。本文档不重新设计 WorkItemCreator，而是把 Unified Intake 归并进当前已合并的 P1 代码基线，让它成为 W0 的实际交付物，并在此基础上补齐 Unified Intake 首期明确排除的 Problem/Change 两个域、KAF 侧建单客户端、以及 Incident typed actions。

本文档是治理总设计，不是覆盖全部 Phase 的实施计划。只有 Phase 1 在本文档中给出足够实施细节；Phase 2-4 只定边界和依赖，各自在启动前另行 `brainstorming` 和 `writing-plans`。

## 二、不可变架构约束

以下约束来自 AGENTS.md 与既有设计文档，后续实施不得重新选择或弱化：

1. WorkItem（`tickets` 表）是共享字段的唯一权威来源；专业扩展表只保存领域专属字段。
2. 一个业务概念只保留一个权威实现——`workitemnumber.Allocator`（P1-A）是编号分配的唯一实现，不允许 Unified Intake 保留自己的 `IncidentNumberAllocator` 作为第二套编号来源。
3. BPMN 是审批、履约、自动化编排的唯一权威引擎；Unified Intake 只负责创建边界，不重做已验收的 KAF 委派/完成协议（`2026-08-28` 设计 §12.8 已确认的门禁顺序）。
4. KAF 负责智能决策（NLU、Procedure/Tool 选择）；ITSM 负责确定性裁定（权限、租户、事务、持久化）。KAF 不得直写 ITSM 数据库或绕过 BPMN/领域服务。
5. 迁移、schema、routing 等高冲突面在同一时间只能有一个 owner；不同 Phase 使用独立 worktree，集成回同一分支前必须互相知会（本次会话因为没有做到这一点，多花了数轮人工确认才收敛，参见治理纪律一节）。
6. 不接受长期 dual-write、双套创建路径或 deprecated 兼容层；旧路径在同一改动里删除。

## 三、已核实的当前基线

| 主题 | 代码事实（本次逐行核实，非文档自述） | 结论 |
|---|---|---|
| Unified Intake 实施状态 | `feat/kaf-delegation-transactional-delivery`（fork 于 `fda84251`，P1 之前）已实现 `handlers/intake` 全套（Application Service、Creator Registry、幂等、身份交换、SLA 解析、Outbox 启动 BPMN），`go test ./... -count=1` 在该分支自身状态下可复现通过 | 真实存在，不是空文档；但完全没有 P1 的任何提交 |
| 编号生成器冲突 | Unified Intake 自带 `IncidentNumberAllocator`/`GenerateIncidentNumberForIntake`（编号格式 `TKT-E2E-NNNNNN`），与已合并的 `workitemnumber.Allocator`（`TKT-YYYYMM-NNNNNN`）是两套独立实现 | 需要在 Phase 1 里删除前者，改用后者 |
| 迁移号冲突 | 两条线都独立使用了 `020`/`021`/`022`：main 上是 `020_work_item_number_allocator`/`021_add_callback_optional_declared`/`022_drop_professional_extension_shared_fields`；Unified Intake 分支是 `020_unified_intake_rls`/`021_work_item_authority`/`022_external_identity_version` | 需要重新编号，且 `021_work_item_authority` 需要拆分（见下） |
| `021_work_item_authority` 内容重叠核查 | 该迁移对 `incidents` 表的字段删除（`title/description/status/priority/reporter_id/tenant_id/created_at/updated_at`）是 P1 已合并的 `022_drop_professional_extension_shared_fields` 的**子集**（P1 版本多删了 `assignee_id/category/subcategory/source/version/resolved_at/closed_at/deleted_at`）；但该迁移对 `service_requests` 表的字段删除、RLS 策略重写，以及 `service_catalogs.itsm_type` 退役 + `target_class` 收口为 NOT NULL，**P1 完全没有触碰** | `incidents` 部分丢弃（已被 P1 取代）；`service_requests`/`service_catalogs` 部分是真实未完成工作，需要保留并重新编号 |
| `service_catalogs.itsm_type` 现状 | `ent/schema/servicecatalog.go` 在 merged main 上仍有 `itsm_type` 字段（`Default("Request")`），`target_class` 字段注释写着"Wave 2 由 itsm_type 迁移填充，本阶段只加列" | 与 08-30 总设计 W0 表格"Catalog 的 target_class 仍受遗留 itsm_type 同步逻辑影响"完全对应，确认是真实、仍悬空的债务 |
| 身份交换基础设施 | `external_identities` 表、identity exchange 端点（`aud=itsm-intake` 短期 JWT）目前只存在于 Unified Intake 分支，merged main 上零匹配 | Phase 1 完成后，这套基础设施会随之落地到 main，是 Phase 3（KAF 建单客户端）的前置依赖，不需要额外补建 |
| KAF 侧建单客户端 | KAF 仓库（`D:\SynologyDrive\kerry\KAF_Migration_Pack\kaf-main`）里没有任何调用 `/api/v1/intake/work-items`、`CreateWorkItemCommand` 或 `idempotencyKey` 字段的代码；ITSM 侧 Unified Intake 实施报告声称的 KAF 切换提交 `16b1bae` 在 KAF 仓库里不存在 | KAF 侧建单客户端是真实未开始的工作（Phase 3），但**不影响** SSLVPN 委派/执行链路——该链路的建单入口是 ITSM 自己的 Service Request API，已经过真实 Azure AD 加组验收 |
| Incident typed actions | `2026-08-28` 设计 §12.3 确认：当前已合并的 action API 只接受 `complete_bpmn_task`/`update_progress`/`record_execution_failure`；`assign`/`resolve`/`close` 尚未实现；`AssignIncident` 目前没有版本校验和状态机守卫 | Phase 4 范围，且必须先补 `AssignIncident` 的状态/版本守卫才能承接 typed action |

## 四、总体架构与 Phase 划分

```text
Phase 1 (ITSM)  Unified Intake × P1 归并
  └── Phase 2 (ITSM)  Problem/Change ProfessionalCreator + SLA 绑定
  └── Phase 3 (KAF)   对话式建单客户端（调用 /api/v1/intake/work-items）
  └── Phase 4 (ITSM)  Incident typed actions（assign/resolve/close）
```

| Phase | 目标 | 依赖 | 代码库 |
|---|---|---|---|
| 1 | 归并 Unified Intake 到 P1 基线，成为 Incident/Service Request 的权威创建入口 | 无（基于已合并的 main） | itsm-backend |
| 2 | 复用 Phase 1 的 Creator Registry，给 Problem/Change 补 ProfessionalCreator + SLA 绑定 | Phase 1 | itsm-backend |
| 3 | KAF Web/Teams/WeCom 渠道通过身份交换 + `CreateWorkItemCommand` 建单 | Phase 1（契约需先稳定） | KAF（`kaf-main`，独立仓库） |
| 4 | `IncidentService` 支持调用方 `expectedVersion`，接入 KAF 的 `assign`/`resolve`/`close` | Phase 1（复用 S0/P1-C 的 `authorizeKafAutomationActor`/`BPMNAccessScope`） | itsm-backend |

Phase 2 与 Phase 3 互相独立，可并行；Phase 4 风险面最独立，也可与 2/3 并行。并行的前提是**每个 Phase 一个 owner、一个独立 worktree，集成回共享分支前互相知会**——这是本次会话在归并 P1 内部两条分叉分支（`feat/p1-architecture-integration` 与 `fix/p1-final-approval-authority`）时的直接教训，不是理论建议。

Phase 2-4 只在本文档中确定边界和依赖，不在此展开实施细节；各自启动前必须先有独立的 `brainstorming` 结论和 `writing-plans` 输出。

## 五、Phase 1 详细设计

### 5.1 目标

把 `feat/kaf-delegation-transactional-delivery` 的 Unified Intake 实现（`handlers/intake` 全套）归并到当前 `main`，解决编号生成器和迁移号两处冲突，并让 ITSM 自己的 `/incidents`、`/service-requests` 入口也统一走这条创建路径。完成后，W0（WorkItemCreator + SLA policy binder，覆盖 Incident/Service Request 两个域）视为交付完成。

### 5.2 架构决策

**归并方式**：以 `feat/kaf-delegation-transactional-delivery` 为基础在独立 worktree 中 rebase 到 `main`，而不是反向把 P1 的改动拣选进 Intake 分支——Intake 分支的提交数更少（14 个 ITSM 侧提交 vs P1 的上百个提交），rebase 的冲突面更小。

**编号生成器**：删除 `handlers/intake` 内的 `IncidentNumberAllocator` 接口、`GenerateIncidentNumberForIntake` 及其实现；`IncidentCreator`、`ServiceRequestItemCreator` 的构造函数改为直接接受 `workitemnumber.Allocator`（P1-A 定义的接口，签名 `Allocate(ctx, client, tenantID, issuedAt) (string, error)` 已经满足两个 Creator 的测试替身需求，不需要适配层）。`docs/reports/2026-08-31-unified-intake-implementation-report.md` 中记录的 `TKT-E2E-NNNNNN` 编号格式验收证据作废，Phase 1 完成后的编号格式统一为 `TKT-YYYYMM-NNNNNN`。

**迁移整合**：不做整体重新编号，按内容拆分：

| 原编号/内容 | 处理方式 | 新编号 |
|---|---|---|
| `020_unified_intake_rls`（`intake_requests`/`intake_resolution_snapshots`/`external_identities` 三张新表的 RLS） | 原样保留，新表无冲突 | `023_unified_intake_rls` |
| `021_work_item_authority` 的 `incidents` 字段删除 + 该表 RLS 重写部分 | **丢弃**，已被合并的 `022_drop_professional_extension_shared_fields` 取代（后者删除的列是前者的超集） | 不再存在 |
| `021_work_item_authority` 的 `service_requests` 字段删除 + RLS 重写 + `service_catalogs.itsm_type` 退役/`target_class` 收口部分 | 保留，是真实未完成的工作 | `024_service_request_work_item_authority`（改名，去掉已废弃的 incidents 部分） |
| `022_external_identity_version`（`external_identities` 加乐观锁版本列） | 原样保留 | `025_external_identity_version` |

`024_service_request_work_item_authority` 的 SQL 需要从原 `021_work_item_authority` 中删除对 `incidents` 表的所有 `DROP COLUMN`/policy 重写语句及其前置一致性校验（`IF EXISTS ... incidents ...` 那段 `DO $$` 块），只保留 `service_requests` 与 `service_catalogs` 相关部分；`service_requests` 的一致性前置校验（`LEFT JOIN tickets ... WHERE t.record_class <> 'service_request_item'`）予以保留。

**Schema 全量核对**：Phase 1 的第一个任务必须对整个 Ent schema 目录做一次两条线的全量 diff（不只是本文档已核实的这两张表），确认没有遗漏的表级冲突，再进入实施。

**控制器切换**：按 Unified Intake 设计 §13.1 第 3-5 步，现有 `/incidents`、`/service-requests` Handler 保留公共 URL/DTO 形状，内部改为向 `CreateWorkItemCommand` 做薄转换，要求 `Idempotency-Key` header；同一改动删除被替代的独立事务、提交后 `FieldValue` 写入、BPMN goroutine 启动等旧逻辑。

### 5.3 验收标准

1. `workitemnumber.Allocator` 是 Incident、Service Request、Ticket 唯一编号来源；`rg` 扫描确认 `IncidentNumberAllocator`/`GenerateIncidentNumberForIntake` 零命中。
2. 迁移 `023`/`024`/`025` 在空白 PostgreSQL 上可顺序应用；`024` 的 `service_requests`/`service_catalogs` 变更通过真实数据验证；`itsm_type` 列被删除，`target_class` 为 NOT NULL 且有 CHECK 约束。
3. Unified Intake 原有的单元测试、PostgreSQL 集成测试（含并发/幂等/回滚）、真实 HTTP E2E 全部在 rebase 后的代码上重新跑通，不是复用旧证据。
4. `/incidents`、`/service-requests` 现有 API 的公共请求/响应形状不变，但内部路径统一到 `CreateWorkItemCommand`；`go test ./... -count=1`、`go build ./...`、前端 `tsc --noEmit` 全部通过。
5. Ent schema 全量 diff 报告写入验收证据，确认无遗漏冲突。

## 六、Phase 2-4 边界（不展开）

- **Phase 2**：`ProfessionalCreator` 接口对 Problem、Change 各实现一次；SLA 绑定复用 Phase 1 为 Incident/Service Request 建好的解析逻辑。不扩展到 Catalog Task/Known Error（沿用 Unified Intake 自己的非目标边界）。
- **Phase 3**：新增 KAF 命名空间（`acp/itsm_delegate/` 或职责更清晰的等价划分，避免与现有 `acp/itsm/`——遗留紫羚系统客户端——混淆），实现身份交换 + `CreateWorkItemCommand` 调用；不改动已验收的委派/执行协议。
- **Phase 4**：先补齐 `AssignIncident` 的状态机/版本守卫，再扩展 `ResolveIncident`/`CloseIncident`/`AssignIncident` 方法签名支持调用方 `expectedVersion`，最后接入 KAF typed action；复用而非新建 `authorizeKafAutomationActor`/`BPMNAccessScope`。

## 七、治理纪律

1. 每个 Phase 使用独立 worktree 和独立分支，禁止在同一 Phase 内多个 Agent 各自新开"final"分支收尾——本次会话为了归并两条独立收尾的 P1 分支（`feat/p1-architecture-integration` vs `fix/p1-final-approval-authority`），额外花费了一整轮 cherry-pick/rebase 才收敛。
2. 涉及 `git reset --hard`/`git clean` 的恢复步骤，在计划里提前标注需要一次人工确认，不要等到被 auto-mode classifier 拦截时才现场处理。
3. 任何"已完成/已验证"的表述必须能追溯到具体命令、退出码和测试名称；不采信文档自身的状态声明（本文档"已核实的当前基线"一节的每一行都对应过一次独立验证，包括发现 Unified Intake 实施报告里 KAF 侧切换提交是不存在的）。
4. Phase 1 完成后，`2026-08-30-architecture-assessment-remediation-execution-plan-design.md` 的 W0 应标注为已交付，指向本文档，不再单独排期。

## 八、关联文档

- [AGENTS.md](../../../AGENTS.md) — 架构、租户、安全与 WorkItem 契约
- [2026-08-30-architecture-assessment-remediation-execution-plan-design.md](./2026-08-30-architecture-assessment-remediation-execution-plan-design.md) — W0 的原始条目，本文档是其归并交付
- [2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md](./2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md) — KAF 委派与统一受理的原始设计，含 §12 实施状态权威说明
- [2026-08-31-unified-intake-design.md](./2026-08-31-unified-intake-design.md) — Unified Intake 详细设计，本文档 Phase 1 的归并对象
- [2026-08-31-kaf-delegation-release-closeout-design.md](./2026-08-31-kaf-delegation-release-closeout-design.md) — 已验收的委派/执行链路，Phase 1-4 均不得重做

# SSLVPN End-to-End Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** accepted — 用户已要求原 Agent 重新接手；正在审查接手期间的增量并继续 A3b/A4，尚未完成端到端验收。暂停时基线见[开发交接报告](../../review/2026-09-05-sslvpn-development-handoff-report.md)；保留全部未提交改动。

**Goal:** 从 KAF Web 已有收集与确认卡片，经统一 ITSM 创建、两级审批和 KAF 执行，验证外部用户组授权成功并完成业务状态与审计同步。

**Architecture:** ITSM Intake 持有创建事务并通过 Creator Registry 调用专业域规则；KAF 保留对话理解和 Procedure/Tool 治理。复用当前 BPMN/Outbox/KAF Worker 的任务范围执行，不增加平行审批或执行链路。

**Tech Stack:** Go/Gin/Ent/PostgreSQL/Redis、Next.js/TypeScript、KAF Python/FastAPI/SQLAlchemy、现有 KAF Web React、Microsoft Graph。

**Spec:** [已确认设计](../specs/2026-09-05-sslvpn-kaf-intake-end-to-end-design.md)。

## Global Constraints

- “本期不是新增一条 SSLVPN 专用创建路径。”
- “Creator Registry 只分发专业类型，不实现所有专业状态机。”
- “外部查询、模型调用、KAF 投递和通知发送不进入长数据库事务。”
- “ITSM 不再次分类 KAF 已输出的语义。BPMN 不固定 Procedure/Tool。”
- “产品显示‘申请有效期’；自动回收交付前，不承诺到期会自动失效或移组。”
- 到期自动回收、Teams/WeCom、VPN 登录/网段访问均为本期范围外。
- 当前用户创建与 `kaf_automation` 执行凭据分开；tenant/workspace/用户不能由浏览器自由指定。
- 分支/worktree 隔离；不修改其他工作区文件，不向 main 直接提交。当前 KAF `uv.lock` 修改保留。
- 每个替换面的旧写路径在同一可发布批次删除；中间开发提交不单独部署。
- 涉及共享数据库/外部变更的任务先记录目标、备份/恢复和验证步骤；故障测试使用隔离库。

## 1. 三份子计划及执行次序

| 子计划 | 对应工作包 | 独立完成结果 |
| --- | --- | --- |
| [A：ITSM 统一创建与 Catalog 校验](2026-09-05-sslvpn-itsm-intake-foundation.md) | W0、W1、W2 | 所有创建入口复用同一事务、编号和规则，目录配置可验证 |
| [B：KAF 用户受理接入](2026-09-05-sslvpn-kaf-web-intake.md) | W3 | 原确认卡片可靠创建新 ITSM 申请，身份、重复和超时恢复有契约测试 |
| [C：授权执行与运行验收](2026-09-05-sslvpn-fulfillment-validation.md) | W4、W5 | 真实外部授权、ITSM 回执、KAF 展示和部署验收完整 |

执行方式：已获准进入实现，按 executing-plans / subagent-driven-development 在同一会话顺序推进；每次仅一个实现 Agent 写代码，并由独立审查者复核。当前任务进度见各子计划，未完成的集成门禁保持开放。

```text
A1 复用审查/入口清单
 → A2 契约/依赖边界
 → A3 事务与 Creator
 → A4 全入口切换
 → A5 Catalog 版本/preflight
 → A6 身份交换与公开路由
 → C1 授权策略/有限期限/结果契约（前移，供目录发布与受理使用）
 → A7 集成门禁
 → B1-B4 KAF 接入
 → C2-C4 授权执行与验收
```

B 的开发可在 A2/A6 契约冻结后使用 mock ITSM 开始，但不能在 A7 未通过前宣称真实集成成功。C1 依赖 A3/A5/A6，先交付 ITSM 策略、数据模型和结果契约；KAF 新客户端中的对应类型由 B1 消费该契约。C2-C4 的跨端执行与回归依赖 A、B 集成完成。

### 阶段交付检查点

| 检查点 | 必须具备的可审查结果 | 未满足时的处理 |
| --- | --- | --- |
| A1 完成 | 来源分支逐文件处置、生产入口清单、迁移冲突及 DTO 字段映射 | 继续补清单，不开始整分支归并 |
| A2 完成 | 单一公共创建 port、OpenAPI、严格请求模型及摘要测试 | 保持契约修改与所有调用方在同一批次 |
| A3-A6、C1 完成 | 完整创建事务、全入口接线、身份与读权限、可发布的授权目录策略 | 失败能力明确不可用，不绕过发布校验 |
| A7 完成 | 隔离 PostgreSQL 的迁移、并发、租户及全入口证据 | 不向 KAF 提供“已验收”基线 |
| B4 完成 | 现有卡片以真实用户创建申请，丢响应恢复为同一编号 | 不启动外部授权正例验收 |
| C4 完成 | 两级审批、外部成员关系、原子回执、重放、测试清理证据 | 未闭合的关键问题保持 No-Go |

以上检查点用于提供提交和测试证据，不要求重复确认已经批准的业务范围。代码按既定范围继续推进；共享环境的具体变更仍遵守仓库治理规范。

## 2. 固定基线与可复用成果

| 对象 | 已读取基线 | 用法 |
| --- | --- | --- |
| ITSM main | `5b2dd2c6` | 当前代码事实 |
| ITSM 归并分支 | `worktree-unified-intake-p1-reconciliation` / `35d1958e` | 优先审查 Intake、Change Creator、targetClass 与迁移成果 |
| 早期 Intake 分支 | `feat/kaf-delegation-transactional-delivery` / `0b3858d9` | 归并分支缺失的 handler、identity exchange、干预与测试来源；逐文件审查 |
| KAF main | `d07a178` | 当前对话、确认卡片、Graph 工具与委派 pipeline |

已有成果不能直接整分支覆盖 main。A1 记录每个候选文件的复用、重新实现或排除理由，尤其保留 main 后续 KAF Worker 改动。迁移编号以执行基线注册表为准，不预先抢占其他任务编号。

## 3. 两端公共契约

### 3.1 创建请求

保留已有 Intake 分支的扁平 `CreateWorkItemCommand` 协议；不另建同时支持嵌套/扁平两种 wire 形状的兼容解析器。A2 增补并严格校验以下字段：

```json
{
  "idempotencyKey": "opaque-confirmed-submission-id",
  "intakeKind": "catalog_item",
  "recordClass": "service_request_item",
  "confirmation": "confirmed",
  "title": "SSLVPN access request",
  "description": "Confirmed application summary",
  "catalogItemId": 101,
  "catalogVersion": "sha256-of-canonical-catalog-contract",
  "formSchemaVersion": "sha256-of-canonical-form-contract",
  "formValues": {"vpn_level": "level_1", "access_duration": "duration_30d"}
}
```

示例 ID、标签只用于契约测试；生产内容来自 ITSM 配置。`formValues` key 由目录定义，期限 key 映射为配置化有限时长，代码不按中文标签计算时间。KAF 提交的 recordClass 必须与目标类一致。

统一 `/api/v1/intake/work-items` 从 JSON 接收幂等键；保留的专业 HTTP 创建接口沿旧设计要求 `Idempotency-Key` header，在映射时写入同一内部字段，不维护两份权威键。内部事件从已验证来源的稳定事件 ID 派生键。所有路径缺失键均明确失败，不临时生成随机值。

成功沿用 `CreateWorkItemResult`：`workItemId`、`number`、`recordClass`、`professionalReference`、`workflowStartStatus`、`replayed`。新建 201、幂等重放 200，保持 ITSM `code/message/data` envelope。

### 3.2 身份交换

复用早期分支身份映射与交换服务的职责，但替换尚未上线的签名协议为 v2，不接受旧签名 fallback。`IdentityAssertion` 必须包含 `version=2`、`audience=itsm-intake`、`purpose=create|read` 和 `provider/subject/channel/workspace/eventId/issuedAt/nonce/signature`。HMAC-SHA256 的 UTF-8 输入严格为 LF 连接的 `["2", audience, purpose, provider, workspace, subject, channel, eventId, decimal(issuedAt), nonce]`，无尾部换行；拒绝未知字段、CR/LF、首尾空白和未注册 provider。签名使用小写十六进制并恒定时间比较。

KAF 在服务端从已认证会话、当前 workspace 成员关系生成 assertion；workspace 使用服务端加载的 UUID，subject 使用当前调用者已认证的外部 sub 并核对其本地身份映射，不使用邮箱或本地 user UUID 代替。管理员能查看他人会话不意味着可以为会话主人签名。浏览器不发送待签名的任意 subject/workspace。ITSM 根据受信 provider、workspace、subject 查内部映射，签发短期 `aud=itsm-intake`、`scope=intake:create` 的凭据。

创建 token 不自动用于目录查询或当前状态查询。A6 实现以下最小读契约，复用同一交换服务和身份映射：

| 路由 | 凭据/结果 |
| --- | --- |
| `POST /api/v1/intake/identity-exchange` | purpose=create 的 v2 assertion；固定签发 `aud=itsm-intake`、`intake:create` |
| `POST /api/v1/intake/identity-exchange/read` | purpose=read 的新 v2 assertion；固定签发同 audience 的 `intake:catalog:read intake:workitem:read`，不能创建 |
| `GET /api/v1/intake/catalog-items?q=&cursor=` | catalog read scope + 目录可见性；返回 `CatalogPage` |
| `GET /api/v1/intake/catalog-items/{id}` | catalog read scope + 逐目录权限；返回 `CatalogContract` |
| `GET /api/v1/intake/work-items/{id}` | workitem read scope + 当前用户行权限；返回 `WorkItemView` |

交换用途必须与已签名 purpose 及路由一致，audience 必须为 itsm-intake，不能提交任意 scope/role；provider 配置分别允许 create/read 用途。两端点共用 nonce store，同一 assertion 不能跨端点再用。读凭据 TTL 与原写凭据一致，映射停用/权限撤销后不得继续读取；秘密只在服务端。读 scope 不可访问通用业务 API，写 scope 不隐式包含读权限。

A2/A6 的 OpenAPI 固定以下最小投影，B1 使用同名严格模型：

- `ExchangeResult`：`token: string`、`tokenType: "Bearer"`、`expiresIn: integer`、`scope: string`；绝不进入 pending context。
- `CatalogPage`：`items: CatalogSummary[]`、`nextCursor: string|null`。`CatalogSummary` 包含 `id: integer`、`name: string`、`description: string`、`targetClass: string`。列表只返回可用且当前用户可见项；游标绑定租户/查询条件，默认最多 50 项。
- `CatalogContract`：Summary 字段 + `catalogVersion: string`、`formSchemaVersion: string`、`fields: CatalogField[]`。字段项为 `key/label/type/required/readOnly/options`；options 为 `key/label` 数组；type 取现有字段定义类型，未知类型明确拒绝。授权目标和期限由目录选项表达，不能让用户提交任意外部组 ID。
- `WorkItemView`：`workItemId/number/recordClass/status/version`、`fulfillmentState`、`accessResult`。status 保留专业域原值；fulfillmentState 为 `awaiting_approval/fulfilling/unknown/completed/rejected/cancelled`。accessResult 在 C1 前为 null，C1 后为授权结果的受权投影，包含 outcome、verifiedAt、expiresAt；不向普通申请人暴露 provider 原始回执或其他用户信息。

内部 JWT 保留 `tokenType=intake` 和 scope 数组；外部交换 DTO 明确改为 Bearer/string scope，不直接搬用旧分支的响应类型。JWT 增加 `mappingId/mappingVersion`，每次请求按签名 tenant 查询原映射、核对版本/启用状态/用户/provider，再查当前用户权限及行范围。映射失效为明确拒绝，存储故障为可观测失败，不伪装成未映射。

assertion 接受截止时间为 issuedAt + maxAge，要求 now 严格早于截止；nonce 保留 TTL 向上取整覆盖剩余接受期，包括允许的未来时钟偏差。nonce namespace 使用 provider/channel/workspace/nonce 的无歧义编码，不含 purpose；禁止相同 assertion 跨用途重用。交换响应丢失时生成新 nonce/assertion，业务提交键不变。

目录版本字段冻结已确认定义，当前权限始终实时校验；不会因持有旧快照继续授权。

### 3.3 错误语义

| HTTP/错误 | KAF 行为 |
| --- | --- |
| 400 字段/确认/键错误 | 显示结构化错误，修改后新确认 |
| 401 用户身份失效 | 重新认证/交换，保持原提交键 |
| 403 权限或映射拒绝 | 停止自动重试，不 fallback |
| 409 同键不同内容 | 保留原回执，要求用户显式修改后重新确认 |
| 409 目录版本变化 | 获取新定义并重新展示卡片，不静默重写旧请求 |
| 5xx/连接中断/响应丢失 | 持久化未知提交结果，使用原键查询或重放 |

错误响应沿用 main 的数字 `code/message/data` envelope；不导入早期分支独有的字符串顶层 TypedFail。`data` 为 `{errorCode: string, retryable: boolean, fieldErrors?: [{field, message}]}`，其中 errorCode 使用公共创建领域的错误分类。HTTP 400 使用既有 1001/1002，401→2001，403→2003，404→4004，409→4090，500→5001，503→5003；领域错误字符串与顶层数字业务码职责不同。KAF 先读取 HTTP/数字 code，再按 data.errorCode 区分同键冲突与目录版本冲突。

错误契约在 A2 的 OpenAPI 文件和 Go/Python fixture 中统一，禁止两端各自猜字符串。

## 4. 全局完成门槛

- [ ] A1 入口清单覆盖 HTTP、Catalog、BPMN、邮件、AI、Connector、模板、标准变更和 Problem；没有未解释直写。
- [ ] 每个 WorkItem 编号只有统一 allocator；每个专业扩展与基表同事务。
- [ ] 字段、SLA、审计或启动记录任一失败均不留残缺申请。
- [ ] KAF 确认快照与提交一致；取消、过期、重复、响应丢失均有测试。
- [ ] Catalog 发布校验与运行时复验生效；没有 Procedure/Tool 镜像策略。
- [ ] 两级审批拒绝或权限失败不会产生外部授权。
- [ ] 外部成员关系查询确认成功，结果与回执/审计一致；重放不新增副作用、不改变首次时间。
- [ ] 两 Worker、真实数据库、跨角色拒绝、外部清理均有证据；自动回收未实现不被写成已交付。

## 5. 集成与交付纪律

各任务按“红测试 → 最小修改 → 绿测试 → 独立可审查提交”执行，提交使用 Conventional Commits。新生成 Ent 代码与 schema 一起检查；编译和契约改变必须更新全部受影响调用方。

A/B/C 每个子计划结束跑自身完整门禁；全系统交付前再次运行真实路径，不用旧报告替代。按治理要求由独立审查者或维护者复核 WorkItem、权限、迁移、BPMN 和外部执行变更。

所有运行证据写到 `docs/review/2026-09-05-sslvpn-end-to-end-verification-report.md`：记录提交、命令、退出码、数据库类型、跳过项、外部变更标识、恢复结果。文件在实际执行后生成；本计划不预填通过结果。

## 6. 计划追踪

| 设计章节 | 任务 |
| --- | --- |
| §6 KAF 用户提交 | A6、B1-B4 |
| §7 创建事务和领域职责 | A2-A4 |
| §8 全部创建入口 | A1、A4、A7 |
| §9 Catalog 校验 | A5 |
| §10 审批/执行/时间 | C1-C3 |
| §11 故障与并发 | A3/A7、B2/B4、C2/C4 |
| §13 迁移与外部验收 | A7、C4 |

实施中若发现新入口、缺失外部能力或与当前接口不兼容的事实，先在对应任务加入明确差异和测试，再修改；不以静默 fallback 扩展行为。

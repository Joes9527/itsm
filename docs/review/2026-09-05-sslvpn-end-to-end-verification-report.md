# SSLVPN 端到端实施与验证报告

**状态：实施中，尚未达到端到端验收门槛。** 本报告从已执行的验证开始记录；阶段测试通过不等于完整业务交付，也不代表已部署。后续任务应在此追加最终提交、命令、退出码和验收证据。

## 1. 验收范围与当前进度

依据[实施计划](../superpowers/plans/2026-09-05-sslvpn-end-to-end-implementation.md)和[已确认设计](../superpowers/specs/2026-09-05-sslvpn-kaf-intake-end-to-end-design.md)，最终链路为：KAF Web 理解意图并复用收集/确认卡片 → 当前用户通过 Unified Intake 创建 ITSM 申请 → BPMN 两级审批 → Worker 委派 KAF 执行 → 查询确认外部用户组成员关系 → ITSM 记录授权结果、状态、审计 → KAF 展示结果。

成功标准是**外部用户组授权经查询确认成功**。权限到期自动回收仍为 Backlog；本期不验收 VPN 登录、网络连通性或 Teams/WeCom。

| 阶段 | 已有证据 | 尚未完成 |
| --- | --- | --- |
| 入口与字段盘点 | [归并清单](2026-09-05-intake-reconciliation-inventory.md) | 最终逐入口处置与全链路回归 |
| Intake 契约与创建事务 | 公共创建 port、Registry、严格输入、幂等回执、事务重试和配置快照已通过阶段审查 | 全入口及共享字段切换后的整体审查 |
| 持久化流程启动 | 精确流程定义、启动摘要、Outbox 重放及故障恢复已有阶段证据 | 生产装配完成后的完整流程验证 |
| Incident 创建事件 | 复用原规则引擎、事务动作与事件回执，已有阶段审查 | 与全部入口、运行角色的最终联合门禁 |
| 数据库租户隔离 | 实际受限角色、Tenant/System 独立连接、真实 RLS 策略测试及独立复审通过 | 最终迁移组合与实际服务进程启动门禁 |
| 全入口、旧路径和共享字段归并 | HTTP、Catalog、BPMN、邮件、AI、Feishu 等适配已产生内部提交及针对性测试 | 旧创建 API 退役、共享 schema/读取方迁移、最终独立审查进行中 |
| ITSM 前端与目录发布 | 已准备最终接口适配及验收清单 | 尚未完成该阶段实现与验证 |
| 用户身份交换、KAF 确认卡接入 | 已有明确接口与安全契约 | 尚未完成本期实现与集成门禁 |
| SSLVPN 授权、回执与外部验收 | 已确定策略、有限期限、结果契约和验证方案 | 尚未执行本期完整业务验收 |

## 2. 已审查的后端前置提交

| 提交 | 覆盖内容 | 证据边界 |
| --- | --- | --- |
| `62fec316`、`ffc4d17d` | 创建事务、当前权限校验、Prepare 顺序、配置快照与整事务重试 | 不代替全部真实入口验收 |
| `00034817`、`c1586c7e`、`56bc3df9` | 持久化精确流程启动、回执与重放；迁移 023 | 初始启动测试不能证明任意后续节点或定义编辑行为 |
| `e0055b83` | Incident 创建事件及规则动作事务；迁移 024 | 专业生命周期仍由 IncidentService 所有 |
| `1808f2d9`、`367f9af9` | RLS 驱动、Ent 变量处理、独立受限 System 能力和实际运行构造 | 复审两个 Important 问题均已修复；不等于共享环境已启用 enforce |

后续入口归并中的 `9850c1d6`、`a9986ccb`、`4acfcfcb`、`548d34e3` 为**开发检查点，整体尚未独立审查**。其中新增了迁移 025、目录确认版本读取、统一创建响应、持久化精确数字和单一 Feishu 创建意图。不得将中间提交单独部署或作为最终发布依据。

## 3. 可定位的 RLS 验证记录

以下结果对应 `367f9af9` 阶段的稳定代码，工作目录为 `itsm-backend`。数据库类测试使用专属临时 PostgreSQL 16 实例 `127.0.0.1:36444/sslvpn_test`；环境变量 `ITSM_TEST_DB` 和 `INTAKE_POSTGRES_TEST_DSN` 指向该隔离库，不使用共享数据库。

| 命令 | 退出码/结果 |
| --- | --- |
| `go test -json ./common/tenantctx ./database ./database/rls ./config ./authentication ./authorization ./handlers/common ./middleware ./service ./internal/bootstrap ./migration ./tests/contract ./tests/rbac ./controller ./router -count=1` | 0；15 个包、3,033 条 test/subtest 通过事件、7 个既有跳过项。跳过不计为通过证据 |
| `go test -tags integration_postgres ./tests/integration -run 'TestPostgresRLS\|TestPostgresIncidentEffectsRLSUnderNonBypassRole' -count=1 -v` | 0；11 个受影响 PostgreSQL 测试通过，无跳过，22.004 秒 |
| `go test -tags integration_postgres ./tests/integration -run TestPostgresRLSRuntimeStatementsAndTransactions -count=1 -v` | 0；验证跨租户 INSERT 被实际 RLS 策略拒绝，同租户 INSERT 成功 |
| `go test -tags integration_postgres ./tests/integration -run TestPostgresRLSSystemCapabilityConstruction -count=1 -v` | 0；验证独立凭据构造、缺失/过量权限、错误所有权和业务访问拒绝 |
| `go test ./config -count=1` | 0；独立文件秘密配置及错误配置场景 |
| `go test ./database/rls -count=2` | **失败**；既有 helper 重复 `sql.Register` 导致 panic。曾被后续命令的退出码误判，已更正；没有整体重复运行通过的证据 |

`-count=2` 的 helper 重复注册问题已作为 Minor 保留给后续维护/最终审查；不覆盖上述独立 `count=1` 和真实 PostgreSQL 的通过结果。数据库测试创建的专属临时角色/schema 已在该阶段清理并检查零残留。

## 4. 对调用方的明确变更

- 保留的专业创建 HTTP URL 统一返回 `CreateWorkItemResult`：`workItemId`、`number`、`recordClass`、`professionalReference`、`workflowStartStatus`、`replayed`，沿用数字 `code/message/data` envelope。新建 201，重放 200；不继续返回旧 `id`/`ticketId`/完整详情别名。
- 专业 HTTP 创建接口由调用方持有 `Idempotency-Key`；统一 Intake 接口使用 JSON `idempotencyKey`。响应丢失或身份刷新后的重试必须保持原键和确认内容。
- 目录详情提供确认用 `catalogVersion`/`formSchemaVersion`；重试不能静默读取新版并替换原确认快照。
- 共享创建字段归于单一根命令，严格契约当前为 `intake-v4`；所有活跃客户端必须随同发布批次升级。
- 工作流启动待处理不代表审批完成或外部授权成功。详情读取失败也不能把已提交的创建回执解释为创建失败。

这些是需要协调升级的响应契约变更；前端与 KAF 最终验收通过前，本批次不满足可发布条件。

## 5. 环境、外部影响与后续证据

- ITSM 和 KAF 使用独立 feature worktree；原 KAF 工作区已有 `uv.lock` 修改保持原样。
- 专属临时 PostgreSQL 容器使用本机端口 36444。专属 Redis 使用 `127.0.0.1:36445`，健康检查 PONG，关闭持久化；ITSM/KAF 分别预留 DB0/DB1。依赖健康不计为 API、Worker 或业务链路通过。
- 未向共享数据库或现有角色写入本期变更，未推送、合并或部署，未执行本期 Graph 成员关系查询或外部授权变更。
- 后续必须补充：最终 schema 升级/恢复与 Ent 重启检查、每个真实创建入口、Catalog 发布组合校验、当前用户身份与撤权、确认卡丢响应恢复、两级审批、两个 Worker、外部成员关系核验、首次授权时间重放不变、回执/审计一致性、外部测试清理。
- 流程实例等待期间修改同一流程版本的行为尚需 A5 具体验证；不能由精确初始启动摘要或固定定义下的数值精度测试推出流程定义不可变。

最终验收应记录当时的提交、完整命令及独立退出码、跳过项、数据库/角色模式、外部测试对象与变更标识、恢复结果；未运行的项目持续保持未验证状态。

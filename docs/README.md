# ITSM 文档中心

- [WorkItem 任务分配：实现决策、验证证据与后续顺序](./review/2026-09-15-work-item-task-assignment-report.md)

- [KAF / ITSM 本机 WSL 开发环境](development-environment.md)：当前源码入口、运行关联、启动来源、共享基础设施和回滚材料。

这个目录包含产品说明、部署运维、开发协作、测试报告和阶段性评审文档。为了避免新用户在大量历史文档中迷路，建议先从本页按角色阅读。

## 快速入口

| 角色 | 建议阅读 |
|:---|:---|
| 试用者 | [README 快速开始](../README.md#-快速开始)、[v1.0 GA 收口验收指南](./v1-ga-readiness.md) |
| 部署人员 | [部署指南](./deployment.md)、[配置参考](./configuration.md)、[运维手册](./operations.md) |
| 后端开发 | [开发指南](./development.md)、[数据库说明](./database.md)、[后端 CI](../.github/workflows/backend-ci.yml) |
| 前端开发 | [开发指南](./development.md)、[前端 CI](../.github/workflows/frontend-ci.yml) |
| Coding Agent | [架构与领域契约](../AGENTS.md)、[Agent 工程协作规范](./agent-engineering-governance.md)、[共享工程约定](./engineering-conventions.md)、[开发与运维手册](./DEVELOPMENT_GUIDE.md) |
| 产品/方案 | [开源发布能力说明](./product/open-source-release-capability.md)、[产品文档索引](./product/README.md) |
| 测试/QA | [角色视角测试方案](./testing/role-based-product-test-plan.md)、[测试用例目录](./testing/test-cases/README.md) |
| 发布维护 | [Release workflow](../.github/workflows/release.yml) |
| 文档维护 | [文档命名与维护规范](./documentation-style-guide.md) |

## 核心文档

- [从服务目录到工单流程与 SLA](./product/service-to-work-item-lifecycle.md)：模块职责、完整申请链路、SSL-VPN 示例和配置验收清单。
- [SSLVPN WSL 部署与手工端到端验收](./deployment/sslvpn-wsl-deployment-and-manual-verification.md): 两仓配套部署、正常身份、双审批、授权验证与测试恢复。
- [部署指南](./deployment.md): Docker Compose、生产部署、反向代理和发布部署建议。
- [配置参考](./configuration.md): 环境变量、端口、数据库、Redis、AI 服务配置。
- [开发指南](./development.md): 本地开发、前后端命令、调试和常见问题。
- [数据库说明](./database.md): 数据库迁移、备份和模型说明。
- [运维手册](./operations.md): 日志、健康检查、备份、恢复和故障排查。
- [v1.0 GA 收口验收指南](./v1-ga-readiness.md): 默认能力、连接器、AI 审计和部署模式检查。
- [文档命名与维护规范](./documentation-style-guide.md): 目录分层、命名规则和归档标准。

## 产品与架构

- [WorkItem 后续实施计划（代码与隔离旅程已验收，退役未完成）](./superpowers/plans/2026-09-11-workitem-next-stage.md)：后端五项任务、公共页面三项任务和跨域验收/退役两项任务的依赖与证据入口。

- [WorkItem 后续收敛设计（accepted，执行中）](./superpowers/specs/2026-09-11-workitem-convergence-next-stage-design.md)：后端契约、页面操作、跨域验收与退役门禁三批设计；包含已确认的三域转派和 backlog 边界。

- [WorkItem 重构后续开发输入与决策记录（accepted）](./superpowers/specs/2026-09-11-workitem-convergence-development-input.md)：本轮讨论总入口，覆盖 Incident、Problem、Change、Requested Item/generic 回归和公共能力；已确认与待讨论分列。

- [Incident 分配与处理动作决策（accepted，B1 已验证）](./superpowers/specs/2026-09-11-workitem-incident-assignment-convergence-design.md)：WorkItem 收敛的分配/转派、可选确认和首次响应 backlog 决策过程。

- [WorkItem 收敛实施总计划（draft）](./superpowers/plans/2026-09-09-workitem-convergence.md)：专业动作、关系联动、流程身份及切换门禁三个子计划。

- [WorkItem 现有实现收敛设计（accepted）](./superpowers/specs/2026-09-09-workitem-convergence-design.md)：Incident、Problem、Change 生命周期、关系、SLA 与 BPMN 的分批收敛。
- [UI 核心路径复核与后续修复顺序（accepted）](./superpowers/plans/2026-09-14-ui-core-journey-recovery.md)
- [UI 工作台 1A 回归修复实施计划](./superpowers/plans/2026-09-14-ui-workbench-1a-recovery.md)

- [主管工作台 2B 能力盘点（draft，暂缓）](./superpowers/specs/2026-09-14-manager-workspace-2b-design.md)
- [工程师工作台 2A 实施与验收](./superpowers/plans/2026-09-14-engineer-workspace-real-data.md)
- [工程师工作台 2A：真实队列与既有处理能力复用（implemented）](./superpowers/specs/2026-09-14-engineer-workspace-real-data-design.md)
- [UI 工作台补齐：背景与第一阶段回归修复设计（accepted）](./superpowers/specs/2026-09-14-ui-workbench-completion-design.md)

- [SSLVPN：KAF 对话受理、统一创建与授权闭环设计](./superpowers/specs/2026-09-05-sslvpn-kaf-intake-end-to-end-design.md)
- [SSLVPN 端到端实施总计划](./superpowers/plans/2026-09-05-sslvpn-end-to-end-implementation.md)
- [AI-Native ITSM 架构解析](./articles/07-ai-native-architecture-guidance-harness-skill.md)
- [开源发布能力说明](./product/open-source-release-capability.md)
- [商业就绪架构评审（已归档）](./archive/reviews/commercial-ready-architecture.md)
- [企业级 v1 就绪度评估](./archive/reviews/enterprise-v1-readiness-2026-06-07.md)
- [工作流控制台诊断与设计](./product/workflow-console-diagnosis-and-design.md)

## 测试与评审

- [WorkItem 本轮实施复核与交接](./review/2026-09-11-workitem-next-stage-implementation-review.md)：已关闭问题、真实验证、代码提交与运行待办。

- [WorkItem 受控退役设计（accepted）](./superpowers/specs/2026-09-11-workitem-controlled-retirement-design.md)：保留历史账本、结构准备、受控删除和删除后恢复的详细契约；审阅修订已通过；代码及隔离验证已实施，Tasks 1–6 分批审阅已通过；最终审阅 I1/I3/I4/M1 已修复、限定复审已完成（接手方复审 PASS，非独立第三方）；I2 已按维护者决定列为 backlog，一般 P 前活动流程退役仍未验收。当前 HEAD 隔离全流程重跑 `workitem-v1-e27375881582` 三时点各 9/9、共 27/27，覆盖 observation、R(038)、独立 Redis 恢复与业务 V1；共享 dev 容器 `itsm-postgres-dev` 专属 DB `workitem-target-20260912102644-8d3920` 已完成真实 CLI 准入（037 fail-closed）、P(037) 与普通迁移并精确清理。真实目标环境部署/退役仍未授权或实施；共享目标完整 P→观察→R→恢复与 focused real-PG 未执行。

- [WorkItem 受控退役实施计划](./superpowers/plans/2026-09-11-workitem-controlled-retirement.md)：六批实施与验证任务；Tasks 1–6 已通过分批审阅，Task 6 验证及审阅状态见计划。
- [WorkItem 受控退役审阅闭环](./review/2026-09-11-workitem-controlled-retirement-review-report.md)：验证顺序、回退依赖与写入前门禁的证据及修订结论。

- [WorkItem 切换与恢复手册](./deployment/workitem-convergence-cutover.md)：P／普通迁移／授权 R 的执行边界、三时点隔离恢复证据、独立 Redis 与受控令牌失效边界及目标环境待执行步骤。

- [WorkItem 受控退役目标环境执行 Runbook（待授权）](./deployment/workitem-controlled-retirement-target-runbook.md)：准入/权限核验/写入者盘点/P→观察→R→恢复步骤、失败决策树与证据模板。

- [WorkItem 后续 backlog 就绪评估](./review/2026-09-12-workitem-backlog-readiness.md)：I2、物理清理审计、Change 多 WorkOrder、首响、RCA/HMAC/Redis/匿名卷的现状与未来验收条件。

- [WorkItem 后续 backlog 设计草案（DRAFT）](./superpowers/specs/2026-09-12-backlog-design-drafts.md)：各项 backlog 的问题、方向、备选、验收与待决问题；未批准、未实现。

- [Shared-target 受控准备执行报告（admission + P + ordinary）](./review/2026-09-12-shared-target-controlled-preparation.md)：共享 dev 容器专属 DB 的真实 CLI 准入/P/普通迁移与清理证据；未执行 R/V1。

- [WorkItem 后续计划独立审查](./review/2026-09-11-workitem-next-stage-plan-review-report.md)：五项重要契约问题、修订及最终复核结论。

- [2026-09-11 WorkItem 收敛独立评审与接手记录](./review/2026-09-11-workitem-convergence-review-report.md)：B/C1 反例、全入口收敛缺口与剩余工作。

- [2026-09-05 ITSM 架构、功能差距与迭代建议（待评审）](./review/2026-09-05-architecture-product-assessment-report.md)
- [角色视角测试方案](./testing/role-based-product-test-plan.md)
- [测试用例目录](./testing/test-cases/README.md)
- [系统功能评审清单](./review/system-function-review-checklist-2026-07-01.md)

历史测试报告与阶段性评审（模块功能复盘、浏览器 E2E/功能测试报告、深度业务测试报告、前端 UX
Review、商用就绪验收报告等）已移入 [archive](./archive/README.md)，仅作历史记录，不代表当前状态。

- [委派权限开通的验证完成契约](./contracts/kaf-verified-access-completion.md)

## CI/CD 与发布

当前保留的 GitHub Actions:

| Workflow | 作用 | 触发 |
|:---|:---|:---|
| [backend-ci](../.github/workflows/backend-ci.yml) | 后端格式、静态分析、构建、测试、Go module 校验 | 后端代码或 workflow 变化 |
| [frontend-ci](../.github/workflows/frontend-ci.yml) | 前端 lint、类型检查、单测、Next.js standalone 构建 | 前端代码或 workflow 变化 |
| [api-contract-check](../.github/workflows/api-contract-check.yml) | 前后端 API 路径与字段命名静态校验 | API client、router 或 workflow 变化 |
| [test-coverage-guard](../.github/workflows/test-coverage-guard.yml) | 校验受管源码变更有对应测试 | 受管前后端源码变化 |
| [GA Gate](../.github/workflows/ga-gate.yml) | 启动核心 Compose 栈并执行健康检查与 API 烟测 | 核心应用或编排变化 |
| [Security Scan](../.github/workflows/security.yml) | gosec、Trivy、npm audit、TruffleHog | main/develop、PR、每周定时、手动 |
| [Build & Release](../.github/workflows/release.yml) | 多平台后端二进制、前端产物、GitHub Release、GHCR 镜像 | `v*` tag |

CI 按后端、前端、契约、集成、安全和发布分层。`ga-gate` 只验证组装后的核心栈，不重复执行前后端单元测试。

## 文档维护原则

- README 只放项目定位、核心能力、最快启动和主入口。
- `docs/README.md` 作为文档导航，不承载大量业务细节。
- 仍有长期参考价值的评审、测试方案保留在 `docs/review/`、`docs/testing/` 等目录。
- 新增长期有效文档时，优先补到本页索引；临时报告使用日期命名，避免和正式指南混淆。
- 历史 bug 报告、过期计划和阶段性复盘统一放入 [archive](./archive/README.md)，避免干扰当前用户路径。

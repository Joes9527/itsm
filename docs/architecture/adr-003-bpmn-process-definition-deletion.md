# ADR-003: 流程定义删除的持久化语义与发布失败原因暴露

## Status

**Accepted**（实现已合并并在 dev 运行）

> 本 ADR 是**事后补齐**的决策记录：实现（PR #50 / `d226c1ce`）先合并，决策比选未在当时落成文本。
> 补记原因与独立复核状态见下文 **Review status** 一节。

## Context

2026-09-17 在共享 dev 库上复现了两个缺陷：

1. **任何"被激活过版本"的流程定义都无法删除。**
   `DeleteProcessDefinition` 只删除 `process_definitions` 一行，未处理 `process_version_changelogs`；
   而该外键是无级联的 `NO ACTION`。变更日志在"激活流程版本"时写入，因此所有激活过的流程永久不可删
   （`workflow_1789626406862`「空白流程」即此例：0 实例、1 条变更日志、删除返回 500）。
   ```
   DELETE /api/v1/bpmn/process-definitions/workflow_1789626406862?version=1.0.0
   HTTP 500
   … violates foreign key constraint "process_version_changelogs_pro_0b6bb8d4725c3197e6bf2c5533474b8f" … (23503)
   ```
2. **实例校验语义与文案不准确。**
   原实现用 `ProcessInstance.Count()`（未按状态过滤），只要存在实例就返回「该流程定义有 N 个运行中的实例」，
   即使 4 个实例全部是 `completed`；前端删除确认框却写「相关的实例将被停止」——前后端语义不一致。
3. **服务目录发布失败不可诊断。**
   `ValidateCreationPublication` 的真实原因（缺流程绑定 / 任务缺审批候选人 / 能力绑定不完整）被包装后丢弃，
   接口只返回 `catalog publication configuration is incomplete`，管理员无法据此修复配置。

## Decision

| # | 决策 | 理由 |
|---|---|---|
| D1 | `DeleteProcessDefinition` 在**同一事务**内先按 `process_definition_id + tenant_id` 删除 `process_version_changelogs`，再删除定义 | 变更日志是**定义自身版本元数据**，定义删除后不存在可解释的归属；同事务保证不留孤儿、失败整体回滚 |
| D2 | 实例校验拆成两类并**拒绝删除**：存在 `created/running/suspended` → 「未结束实例」；仅有终态实例 → 「历史实例，请停用」 | 保留 BPMN 执行历史与审计完整性；**不级联删除** instances/tasks/variables/execution_histories |
| D3 | 冲突返回 **409 + 可读中文原因**（新增 `common.NewConflictStateError`），删除接口改用统一错误映射 `respondBPMNError` | 原先把冲突报成 500 并把原始外键错误泄给用户；显式失败优于静默或误导 |
| D4 | 发布校验失败把 cause 放入 `fieldErrors[publication]`，前端新增 `apiErrorMessage()` 展示后端真实原因；同时修正删除确认文案 | 让管理员能凭错误信息直接定位缺失配置，而不是猜 |

## Alternatives considered

| 备选 | 未采纳原因 |
|---|---|
| schema 级 `ON DELETE CASCADE`（变更日志随定义级联删除） | 需要迁移与兼容性决策；服务层同事务删除已覆盖唯一删除路径，且不改变历史外键语义 |
| 保留孤儿变更日志（外键改 `SET NULL` + 列可空） | 需要迁移；且"无归属的版本日志"对使用者无意义，反而增加查询歧义 |
| 定义软删除（标记 deleted，不物理删除） | 会与既有"删除"语义、唯一键与列表投影冲突，属更大的契约变更，超出本次缺陷修复范围 |
| 允许删除并把终态实例一起级联删除 | 直接违背 AGENTS.md「Preserve definition, instance, task, variable, history, and audit integrity」 |
| 保持 500 与通用英文短语，仅改文案 | 不解决问题：用户仍无法判断是配置缺失还是系统故障 |
| 只修后端、不改前端 | 前端仍显示通用「删除工作流失败」，掩盖真实原因，用户会重复报同样的缺陷 |

## Consequences

**正面**
- 删除路径不再因版本日志失败；定义与其版本日志在事务内一致清理。
- 冲突原因可读、状态码正确（409），前端直接展示后端原因。
- 执行历史与审计不被破坏；"想下架"有明确替代路径（停用）。

**代价与风险**
- **删除能力被有意收窄**：只要存在任何实例（含已完成）就不能删除定义。这是产品语义选择，已在实现与文档中写明。
- `process_version_changelogs` 会被物理删除。若后续合规要求"定义删除后仍保留版本日志"，需要新的保留策略（见下文 **Rollback boundary**）。
- 发布失败的 `fieldErrors[publication]` 会向具备配置权限的管理员暴露流程 key、任务 id 等**配置描述信息**（非敏感数据）；已确认可接受。

## Rollback boundary

- 代码层：`d226c1ce` 可整体回退到 `4de73348` 基线内容；本变更**无 schema 迁移**，回滚不需要数据库操作。
- 语义层：若要把 D2 放宽为"终态实例可删除并级联清理历史"，属**新的产品决策**，必须另开设计（涉及审计保留与实例外键），不能当作热修。
- 数据层：本次修复本身不写入业务数据；被删除的定义/日志（如「空白流程」）**不可恢复**。

## Verification

| 层级 | 证据 |
|---|---|
| 单测（先红后绿） | `TestDeleteProcessDefinitionRemovesVersionChangeLogs`、`TestDeleteProcessDefinitionRefusesUnfinishedInstance`、`TestDeleteProcessDefinitionRefusesHistoricalInstances`、`TestPublicationFailureExposesUnderlyingCause`；前端 `apiErrorMessage` 3 用例 |
| 全量 | `go test ./...` 62 包 ok / 0 FAIL（exit 0）；前端 `tsc --noEmit` + `eslint` 干净 |
| 现场（8080/3010） | 有 changelog 的临时流程删除 200 且两表归零；4 个 completed 实例的定义返回 409「历史实例」且未变更；`service_request_item` 无绑定创建目录返回 400 + `fieldErrors`；补配置后创建目录 200；真实浏览器删除到 toast 显示后端原因 |
| 真实数据 | `workflow_1789626406862`「空白流程」按修复路径删除成功（HTTP 200，定义与日志均归零） |

## Review status（独立复核）

- 实现 PR #50 合并时 **reviews = 0**，由实现者账号自行合并——**未满足** AGENTS.md §7「实现 Agent 不能作为唯一验收者」的控制点。
- 本 ADR 随补记 PR 一起提交，并在该 PR 上**显式请求维护者复核**（重点：§Decision D1 的变更日志删除语义、D2 的删除能力收窄）。
- 在维护者完成复核前，本 ADR 不应被引用为"已通过独立审查的决策"。

## References

- 实现：PR #50 / commit `d226c1ce`（`DeleteProcessDefinition`、`common.NewConflictStateError`、`respondBPMNError`、`preflight.go`、前端 `apiErrorMessage`）
- 测试：`itsm-backend/service/bpmn_process_deletion_test.go`、`itsm-backend/handlers/service_catalog/publication_error_cause_test.go`、`itsm-frontend/src/lib/api/__tests__/http-client-error.test.ts`
- 部署记录：[2026-09-17 dev 部署与配置变更记录](../operations/2026-09-17-dev-deployment-record.md)
- 代码位置：`itsm-backend/service/bpmn_process_engine.go`、`itsm-backend/controller/bpmn_workflow_controller.go`、`itsm-backend/handlers/service_catalog/preflight.go`

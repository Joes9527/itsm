# ITSM 架构评估报告 — 基于 Roadmap 与 AGENT.md 合规审计

> **[已归档]** 本文档的结论已被 [architecture-product-assessment-2026-08-30.md](../../architecture-product-assessment-2026-08-30.md)
> 核实并订正，请以该文档为准。本文档保留作为历史记录。

> 评估日期: 2026-08-13
> 依据: docs/roadmap.md (v1.1 in progress)、CLAUDE.md、AGENTS.md
> 方法: 三路并行代码审计（工程项 / 产品项 / 架构合规）

---

## 一、Roadmap v1.1 实现程度

### 工程项

| 项目 | 目标 | 现状 | 差距 |
|------|------|------|------|
| 测试覆盖率 | 2% → 40% | **12.0%** (service 31% + controller 10.5%) | ❌ 差 28 个百分点 |
| Controller 拆分 | 拆分 4 个大文件 | **0/4 未拆分** (incident 1.5k行 / ticket 1.1k行 / cmdb 1.9k行 / bpmn 1.1k行) | ❌ 未开始 |
| 集成测试 | 4 主题 (RBAC/BPMN/CMDB/SLA) | 2 个测试文件；CMDB 影响分析**完全缺失** | 🟡 部分 |
| itsm-cli/skill/agent CI | 全部入 CI | agent ✅ cli ✅ skill 是 gitignore 的空壳 | 🟢 基本完成 |
| 覆盖率门禁 | 60% 增量 | **无任何百分比门禁**，只有文件变更 guard | ❌ 缺失 |

### 产品项

| 项目 | 现状 |
|------|------|
| Connector marketplace v1 | 🟡 4 个连接器 + 生命周期 API 完成；**Feishu Approval 未实现**、DingTalk 无 inbound IM、前端从不调 `/lifecycle` |
| AI Audit console | ❌ 只有后端 write-only 记录端点；**无列表/审查 API、无前端控制台、无 evaluator 回路** |
| Standard change templates | 🟢 完整（CRUD + instantiate + 3 个种子模板）；缺 OS patch / DB migration 种子内容 |

---

## 二、架构层面缺陷（按严重性）

### P0-1 六个并行审批机制 🔴

CLAUDE.md 明令 "Do not introduce a second approval engine"。现存 **6 套**审批栈：

| # | 机制 | 状态 |
|---|------|------|
| 1 | approval_workflow + approval_record | 活跃 |
| 2 | approval_chain | 活跃 |
| 3 | ticket_approval | 活跃 |
| 4 | BPMN process_approval_decision | 活跃（规范路径） |
| 5 | change 域自己的 ApprovalRecord/ApprovalChain | 活跃 |
| 6 | change_approval_controller + service | 死代码（无路由） |

还有 AGENTS.md 明令禁止的 **bridge 服务**：`legacy_approval_migration_service.go`（新旧模型转换桥）、`srIncidentBridge`（bootstrap 适配器）。

### P0-2 新 handlers/ 层违反 Repository 模式 🔴

- `handlers/incident/handler.go` — HTTP handler 直接 `client.(*ent.Client)` 查库（10+ 处），还跨域查 SLAViolation
- `handlers/standard_change/handler.go` — 无 repository 文件，handler 直接 CRUD，甚至从 HTTP 层 `Change.Create()` 写跨域实体
- `handlers/change/service.go` — 绕过 repository 用 `entClient.Tx`，跨域读 ConfigurationItem/CIRelationship
- `handlers/common/service.go`、`handlers/service_request/service.go` — 同样问题

### P1-1 审计日志缺口 🔴

CLAUDE.md 要求 AI 建议、流程流转、审批、连接器动作、批量操作**必须**产生审计记录。实际只有 3 个地方写审计：
- 工单/事件/变更生命周期流转 — 零审计
- 工单批量删除/导入导出 — 零审计
- 连接器回调/发送 — 零审计
- SLA 自动化、自动化规则 — 零审计
- AI 建议持久化依赖前端主动调 `POST /ai/audit` — 默认不记录

### P1-2 HTTP 响应 snake_case 泄漏 🟠

dto/ 层干净，但大量 `gin.H` 直出绕过 DTO：
- `handlers/incident/handler.go` — `"incident_id"`、`"occurred_at"` 等
- `handlers/ai/service.go` — `"suggested_title"`、`"prompt_version"` 等
- controller 层 283 个 `common.Success` 中有多处 gin.H 捷径

### P1-3 租户隔离缺口 🟠

无 tenant_id 的 schema：
- `prompt_template`（且是孤儿 schema，无服务使用）
- `marketplace_item` / `item_version`（全局列表查询无租户上下文）
- `message`、`knowledge_article_version` 等（靠 FK 继承，需文档化）

### P2 已提交的临时产物 🟡

git 中跟踪的 `.rej` / `.orig` / `.patch` / `.disabled`：
- `itsm-backend/Oops.rej` + `.orig`
- `itsm-backend/service/*.go.orig`（2 个 32-45KB 文件）
- `itsm-backend/fixes.patch`、`fix_cmdb_import_export.patch`
- `itsm-backend/dto/workflow_dto.go.disabled`
- 仓库根 `insert_connector_menu.go` 杂散脚本

### P2 死代码面 🟡

- `handlers/incident/` 未路由（bootstrap 注释明确说明已移除），但代码保留
- Problem 域双实现：`handlers/problem`（路由）+ `controller/problem_investigation_controller`（路由）
- 3 个通知控制器并存
- SLA 至少 3 条计算路径并存（monitor service + ticket_sla + bpmn_sla + escalation）

---

## 三、建议处理顺序

1. **审批机制收敛**：保留 BPMN，退役其余 5 套，删除 migration bridge
2. **审计补齐**：ticket/incident/change 流转 + 审批 + 连接器 + 批量操作写审计
3. **handlers/ 层仓库化**：incident、standard_change、change 的 DB 访问下移 repository
4. **snake_case 清理**：gin.H 响应改 DTO
5. **覆盖率**：先修门禁（60% 增量 gate），再 backfill
6. **清理提交产物**：删除 .rej/.orig/.patch/.disabled

---

## 四、本轮已修复（评估过程中发现并修复）

- escalation_matrix_test / ticket_sla_service_test 签名漂移（SLA Phase 3/5 遗留）
- ticket_lifecycle_integration_test 引用已删除的 SLAPolicy
- ent/migrate/schema.go 过期（external_message_id 缺失）→ 重新生成
- service/bpmn 测试 panic 修复
- 全部测试转绿

# 规范流程初始化批次执行证据（B0 后续）

- 状态：**EXECUTED**（2026-09-14）。
- 目标：容器 `ga-itsm-20260914`，库 `itsm_ga_ready`，owner `ga_owner`，schema `public`，租户 `tenant_id=1`。
- 门禁：`GARevision=d91b587fe3ab40cc863321346d217d258a3a96d8`。
- 目的：消除 B0 拆分留下的**悬空流程绑定**风险（B0 不含 `process_bindings`）。

## 1. 权威源（与固定制品一致）

- 模板来源=任务一固定制品的 `itsm-migrate` 二进制内嵌 `go:embed bpmn/*.bpmn`。
- 校验方式：逐字节包含检查——候选工作区 `candidate-approval-contract/itsm-backend/service/bpmn/*.bpmn` **20/20 与制品内嵌字节完全一致**（本任务基线 `workitem-config-migration` 有 2 个文件较旧，故**不采用**）。
- 绑定来源：固定制品 seed `0788a9bb` 的 `process_bindings`（7 条）。
- 字段映射复刻应用 `BPMNTemplateService.deployTemplate`：`key=文件名`、`version=1.0.0`、`deployment_id=<key>-v1`、`deployed_by=system`、`bpmn_xml` 为 `field.JSON([]byte)` → jsonb 内 base64 字符串。

## 2. 制品与备份

| 项 | 值 |
| --- | --- |
| 生成器 | `scripts/migrate_config_seed/generate_process_sql.py` |
| 生成 SQL sha256 | `472e3ea007b5a53794e8a1bf58bd08746f34d87991bdd883af75a660b13219ef`（53 行） |
| 写入前备份 | `…/itsm-task2-b0-20260914/itsm_ga_ready-pre-process.pgdump`，sha256 `333dcd3531a6ef0f4f4dd724d6486edfc145b20c34dfd4af10d05b00c1822423` |

## 3. 执行与结果

- 流程：复核门禁 → 备份 → 事务回滚预演（成功、计数仍 0）→ 单事务执行 → 幂等复跑 → 校验。

| 表 | 期望 | 实际 |
| --- | ---: | ---: |
| `process_deployments` | 20 | **20** |
| `process_definitions` | 20 | **20** |
| `process_bindings` | 7 | **7** |

## 4. 完整性与幂等

- 悬空绑定（绑定 key 无对应定义）= **0**。
- `bpmn_xml` 非字符串或 base64 解码不含 `<?xml` = **0**。
- 每个 process key 恰好一行 `is_latest=true AND is_active=true` = **true**。
- 幂等复跑新增 `INSERT 0 1` = **0**。
- Phase 1 不变量与账本不变：depts 7975 / users 7862 / roles 36 / ledger 36。

## 5. 写入的 7 条绑定

| business_type | sub_type | process_definition_key | is_default |
| --- | --- | --- | --- |
| generic | — | `ticket_general_flow` | true |
| incident | — | `incident_emergency_flow` | true |
| problem | — | `problem_management_flow` | true |
| change_request | — | `change_normal_flow` | true |
| change_request | emergency | `change_emergency_flow` | false |
| service_request_item | — | `service_request_flow` | true |
| release | — | `release_approval_flow` | true |

## 6. 说明与剩余

- 按应用规范同步语义部署了**全部 20 个内嵌模板**（含未被绑定引用的 `cloud_*`、`*_cn`、`ticket_urgent`、`ticket_assignment`、`service_request_urgent`、`ssl-vpn`），与 `seedBPMNWorkflows` 行为一致；**未导入旧 BPMN**。
- 仍未完成：B1–B6 配置映射批次（分类/资产落位、优先级/矩阵、SLA 日历、路由授权、模块/流程对照），以及路由 20 个未解析用户 ID。
- G-B 仍未满足；本批不触碰历史工单/流程实例/身份/账本。

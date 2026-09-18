# B6 模块/流程对照与差额登记

- 状态：**对照已完成；无目标写入**（本批不含数据迁移动作）。
- 日期：2026-09-14。目标：`ga-itsm-20260914 / itsm_ga_ready`，租户 `tenant_id=1`。
- 前置说明：**本任务不迁移工单历史**；目标 `tickets` 表 0 行。本文件只处理"旧模块 → 新分类维度"的**配置对照**。

## 1. 旧模块 → recordClass 映射（对照，不需写目标）

| 旧模块 | moduleCode | 旧表 | 目标 `recordClass` |
| --- | --- | --- | --- |
| 事件管理 | `IN` | `itsm_order_event` | `incident` |
| 请求管理 | `SR` | `itsm_order_request` | `service_request_item` |
| 服务台 | `SERVER` | `itsm_workorder` | `generic` |
| 知识管理 | `KN` | `itsm_knowledgelist` | **排除**（知识库不属 WorkItem） |
| 问题管理 | （`modules` 无行；见 `priority_levels.moduleId=2`） | — | `problem` |
| 变更管理 | （同上，`moduleId=4`） | — | `change_request` |

- 目标 `tickets.record_class` 默认 `generic`，值与 WorkItem 设计的 6 类一致；`process_bindings.business_type` 已含 `generic/service_request_item/incident/problem/change_request/release`；`service_catalogs.target_class` = `service_request_item`。
- 结论：该映射是**创建/路由时的分类维度**，目标**无需新增表或行**；仅登记口径。

## 2. 差额登记（需产品侧确认，非旧数据迁移）

| 对象 | 现状 | 性质 | 处置 |
| --- | --- | --- | --- |
| `ticket_types` | 目标 **0 行** | **产品内置默认**（应用 `seedTicketTypes` 硬编码 12 个，与前端 `ticket-type-presets.ts` 一致），**不在固定 seed JSON 中**，且与旧系统无对应概念 | **不写入**（用户确认登记为差异）。如需，属新系统自身初始化，不属旧数据迁移 |

> 说明：`ticket_types` 是"工单类型"**配置**（如账号申请、虚拟机申请），不是工单记录。本任务不迁移工单历史，也不会因本项写入任何业务数据。

## 3. 流程侧（与 B0 后续批次的关系）

- `process_bindings` 已在"规范流程初始化批次"写入 7 条，业务类型见 §1 的 6 类；无悬空绑定。
- 旧 BPMN 流程定义（145 定义 / 25 key / 24 模型）**不导入**；仅需把路由引用的流程 key/版本/任务节点对照到已部署的规范模板（B5 路由批次处理）。

## 4. 本批无写入确认

- 未对 `ticket_types`、`tickets`、`process_*`、身份或账本执行任何写入。
- 目标不变量保持：depts 7975 / users 7862 / roles 36 / ledger 36 / CI 46 / 分类 185。

# ITSM 项目架构分析报告

> 分析范围：自定义表单、流程配置、审批链、权限模型
> 分析日期：2026-08-11
> 数据源：源码分析 + 数据库验证 + 运行时日志

---

## 一、权限模型

### 1.1 角色体系双轨断裂（最严重架构缺陷）

系统存在两套互不相交的角色体系：

| 层次 | 角色列表 | 数量 |
|------|---------|------|
| `users.role` 枚举（Ent Schema） | super_admin / admin / manager / agent / technician / security / end_user | 7 |
| DB `roles` 表（Seeder 播种） | it_director / ops_director / sysadmin / security_admin / audit_admin / change_manager / service_catalog_admin / dept_manager / team_lead / l1_support / l2_support / l3_expert / ops_manager / ops_engineer / dba / network_eng / sd_manager / end_user / guest | 19 |

**交集只有 `end_user` 一个角色。** 后果：
- `change_manager`、`service_catalog_admin`、`sysadmin` 等 18 个 DB 角色**完全不可达**——没有任何用户能持有这些角色
- DB 中给这些不可达角色分配的 100+ 个权限码（如 `change:approve` → change_manager）全是**死数据**
- `users.role` 枚举中的 admin/manager/agent/technician/security 在 DB roles 表中**不存在** → PermissionConfigModeDBOnly 下查不到权限 → 落回硬编码 fallback

### 1.2 20 个 resource 在路由使用但 seeder 完全未定义

以下 20 个资源名在 `router.go` 的 `RequirePermission()` 中使用，但在 `seeder.go` 的权限定义中**不存在**——这些路由对所有角色（除 super_admin）都返回 403：

| 缺失的资源 | 涉及路由数 | 影响范围 |
|-----------|-----------|---------|
| `approval_workflow` | 5 条 | 审批工作流 CRUD 全部不可用 |
| `assignment_rule` | 4 条 | 分派规则 CRUD |
| `automation_rule` | 4 条 | 自动化规则 CRUD |
| `cloud_account` / `cloud_resource` / `cloud_service` | 9 条 | 云资源管理全部不可用 |
| `audit_log` | 1 条 | 审计日志不可查看 |
| `menu` / `tenant` / `permission` | 10 条 | 菜单/租户/权限管理不可用 |
| `process_instance` / `task` / `step` | 8 条 | 流程实例管理不可用 |
| `config` / `view` / `widget` | 8 条 | 系统配置/视图/部件管理 |
| `investigation` / `root_cause` / `solution` | 4 条 | 问题调查/根因/解决方案 |
| `tag` | 1 条 | 标签读取 |

### 1.3 L2 ACL 层完全失效

`smart_permission.go` 查询 `endpoint_acls` 表——该表**不存在**（Ent 建的表叫 `endpoint_ac_ls`，为空且有命名差异）。即使表存在也有两个逻辑缺陷：
- ACL 拒绝后不终止，继续落回 L3/L4 → ACL 拒绝可被覆盖
- `GetResourceAndActionFromPath` 硬编码 `aclCache[1]` → 多租户下查错租户

4 层权限模型实际只有 L1(白名单) + L3(URL 推断) + L4(硬编码) 在工作。

### 1.4 ResourceActionMap 缺失关键路径

以下路径在 `ResourceActionMap` 无映射 → L3 URL 推断失败 → 403：

`/api/v1/notification-preferences*`、`/api/v1/approval-workflows*`、`/api/v1/approval-chains*`、`/api/v1/my-approvals*`、`/api/v1/system-configs*`、`/api/v1/menus*`、`/api/v1/tenants*`、`/api/v1/bpmn/tasks*`

审批中心、审批流管理、通知偏好设置、菜单/租户管理——**这些页面对所有非 super_admin 用户 403**。

### 1.5 权限资源名三方不一致

| 路由 `RequirePermission` | `ResourceActionMap` | DB `permissions` 表 |
|--------------------------|---------------------|---------------------|
| `audit:read` | `audit_logs:read` | 都不存在 |
| `config:read` | `system_config:read` | 都不存在 |
| `notification:update` | `notification:write` | 只有 `notification:read` |
| `process_instance:*` | 无映射 | 不存在 |
| `task:read` | 无映射 | 不存在 |

### 1.6 MSP 角色权限完全不可用

`msp_manager`、`msp_tech` 等 MSP 角色在 roles 表中不存在 → `RequireMSPPermission` → `hasResourcePermission` → DB 查不到 → 所有 MSP API 403。

### 1.7 已修复问题（本次调试中修复）

- `end_user` 补了 `ticket_category:read`、`ticket_template:read`、`notification:read`
- `notification:*` 权限定义从无到有（4 条）
- 模板路由 `"template"` → `"ticket_template"` 统一

---

## 二、自定义表单

### 2.1 工单创建路径不做必填校验（安全性严重缺陷）

- `service/ticket_template_service.go:169` `RenderTemplate` 是唯一做 required 校验的函数——**注释自述"无调用方"**
- `service/ticket_service.go:184` 持久化自定义字段值时**从不校验 required**
- 只有 `handlers/service_request/service.go:122` 做 required 校验，且只覆盖 SR 流程
- **后果**：从模板创建工单，不填必填字段也能提交成功

### 2.2 `field_type` 三套词汇表互不一致

| 来源 | 值 |
|------|----|
| Backend schema 注释 | text / textarea / number / date / select / multiselect / boolean / file |
| 前端 `CustomFieldType` | text / textarea / number / date / **datetime** / select / **multi_select** / **checkbox** / **radio** / **user_picker** / **department_picker** |
| 前端 `FieldType` | 上面所有 + time / file_upload / rich_text / cascader / rating / slider / color_picker / divider / section_title |

**后端不做枚举校验**（`field_definition.go:24` 仅 `NotEmpty()`），任何无效类型静默存储。

### 2.3 multiselect/checkbox/radio 选项编辑时损坏（数据丢失 bug）

`tickets/templates/page.tsx:652` 和 `admin/service-catalogs/page.tsx:140` —— 只在 `type === 'select'` 时将逗号分隔字符串转换回 `[{label, value}]` 数组。`multiselect`/`checkbox`/`radio` 的选项在编辑后变成纯字符串 `"a,b,c"`，后续渲染全部损坏。

### 2.4 Ticket Type 整条垂直线是死代码

`service/ticket_type_service.go` + `controller/ticket_type_controller.go` + `dto/ticket_type_dto.go` + 前端 `types/ticket-type.ts` + `TicketTypeFormModal.tsx` —— **全部未注册到路由**。`router.go:1741` 只有硬编码 stub 且有一个 leading space 的 bug（`" Incident"`）。

### 2.5 双写 `custom_field_values`（旧列未删除）

`ent/schema/ticket.go:132` 保留旧的 `custom_field_values` JSON 列。`ticket_service.go:157` 和 `repository/ticket/repository_impl.go:101` 每次创建工单都同时写入此列——两条路径写同一份数据。

### 2.6 FieldDesigner / TemplateEditor 零引用

`itsm-frontend/src/components/templates/FieldDesigner.tsx`（拖拽式表单设计器）和 `TemplateEditor.tsx` ——**没有任何页面 import 它们**。

### 2.7 Incident 类型服务目录跳过自定义字段

`handlers/service_request/service.go:95` —— incident 分支在字段校验前 return，既不校验也不持久化自定义字段。

### 2.8 DTO 无法展示字段类型

`dto/ticket_dto.go:110` `CustomFieldValueResponse` 没有 `fieldType` → 前端 `TicketDetail.tsx:527` 用 `String(field.value)` → 数组变成 `"a,b"`、对象变成 `"[object Object]"`。

---

## 三、流程配置（BPMN）

### 3.1 BPMN 引擎：声明依赖是死代码，自定义引擎有严重缺陷

- **`go.mod` 声明的 `nitram509/lib-bpmn-engine`** —— 全仓库唯一引用在 `pkg/bpmn/engine_adapter.go`，而该文件**没有任何 import 方**
- 实际引擎是自写的 `CustomProcessEngine`（`bpmn_process_engine.go`，2671 行）
- **默认工单模板审批分支不可达（严重 bug）**：`ticket_general_flow.bpmn` 中 Activity_Assign 有两条无条件出线（Flow_2 和 Flow_Approval），`evaluateCondition` 对无条件流恒返回 true → **永远选第一条 → 审批网关分支永不执行**
- **GatewayEngine 是死代码**：`bpmn_gateway_engine.go` 实现了并行/包容网关，**从未被实例化**（仅测试引用）
- 事件类型（timer/boundary/message）**零支持**：UI 可配置但引擎无处理

### 3.2 每次启动都报部署错误

`deployTemplate` 使用固定 ID `{name}-v1`，`process_deployments.deployment_id` 有全局唯一索引。每次重启尝试重新部署同一模板 → 唯一约束冲突 → **每次启动日志都有 `Failed to deploy BPMN templates`**。

### 3.3 三套版本方案并存且互不兼容

| 服务 | 版本格式 | 已知问题 |
|------|---------|---------|
| `BPMNDeploymentService` | `1.0.0` → `1.1.0` → … → `1.3.0` → 重置 | 部署 5 次后撞车 |
| `bpmnProcessDefinitionService` | `1.0.0` → `1.0.1` | 语义化 |
| `BPMNVersionService` | 整数字符串 `"1"`, `"2"` | `Atoi("1.0.0")` → 失败 → version=0 |

### 3.4 前端工作流设计器 DTO 契约完全破坏

- 后端 `bpmn_workflow_controller.go` 直接返回 Ent 模型（snake_case：`is_active`、`bpmn_xml`）
- 前端读 `item.isActive`、`item.bpmnXml` → **永远 undefined**
- **流程列表全显示 DRAFT**，设计器加载**空白画布**
- 违反了 CLAUDE.md 中 "Controller 必须返回 DTO，禁止返回 Ent 模型" 的明确规定

### 3.5 流程实例 ID 类型断裂

- 引擎按字符串 `"PI-{key}-{nano}"` 查询
- 取消/暂停/恢复操作传 `fmt.Sprintf("%d", processInstanceID)`（Ent int ID）
- → **永远"获取流程实例失败"**

### 3.6 部门流程绑定引用 7 个不存在的流程

`InitDefaultBindings` 引用 `release_test_flow`、`change_requirement_flow`、`expense_approval_flow` 等 7 个不存在的模板 key → 静默跳过 → 部门级绑定全部失效。

### 3.7 前端版本页完全失效

`BPMNVersionService.ActivateVersion` 先停用所有版本再按 `Version("1")` 匹配 → 语义化版本 `"1.0.0"` 永远不匹配 → 失败后流程**没有任何激活版本**。前端 `workflow/versions/page.tsx` 因 version=0 全部失效。

---

## 四、审批链

### 4.1 多级审批链是纯展示功能

`service/approval_chain_resolver.go` 解析审批链步骤后，由 `handlers/service_request/service.go` 注入 `formData._approval_chain`。前端 `ServiceCatalogApprovalChain.tsx` 仅作展示，且自述「实际执行以 BPMN 流程为准」——**审批链解析结果从不生成 BPMN 节点**。

### 4.2 审批链 DTO 无法持久化关键字段

- `ent/schema/approvalchain.go` 定义了 `ApprovalType`（会签/或签）、`Threshold`、`OrgScope`、`AmountThreshold`
- `dto/approval_chain_dto.go` 只有 Level / ApproverID / Role / Name / IsRequired
- → **金额阈值、部门范围、会签/并行类型、分组控制保存即丢失**

### 4.3 审批链引用的角色不存在

DB 中 `approval_chains` 引用 `manager`、`it_admin`、`security_admin` —— `it_admin` 不在 users.role 枚举也不在 roles 表 → BPMN 引擎 `resolveRoleCandidates` 返回 0 个候选人 → **任务无人可批**。

### 4.4 审批中心对所有非 super_admin 用户 403

- `GET /api/v1/bpmn/tasks` 路径在 ResourceActionMap 中无映射
- `bpmn` 资源权限码在 permissions 表中不存在
- 前端审批中心页面（`/approvals`、`/approvals/pending`）全部 403

### 4.5 唯一健康的部分：变更审批

`service/change_approval_service.go`（837 行）基于 `change_approval_chains` 实现了真正的多级审批，含身份校验、链状态同步——**但 `change:approve` 权限只授予不可达的 DB 角色**（change_manager/it_director/ops_director/sysadmin）→ 实际无人可批。

---

## 五、交叉问题总结

### 5.1 严重性分级

| 级别 | 问题 | 影响 |
|------|------|------|
| 🔴 致命 | 20 个 resource 权限未定义 | 大量管理页面 403，系统不可管理 |
| 🔴 致命 | 角色枚举与角色表不交 | 所有企业角色不可达，权限分配形同虚设 |
| 🔴 致命 | BPMN 默认模板审批分支不可达 | 工单审批流程永不触发 |
| 🔴 致命 | BPMN DTO 契约破坏 | 工作流设计器完全不可用 |
| 🟠 严重 | 工单创建不做必填校验 | 安全漏洞 |
| 🟠 严重 | multiselect 选项编辑损坏 | 数据丢失 |
| 🟠 严重 | BPMN 版本管理三套互不兼容 | 版本激活/回滚全部失败 |
| 🟠 严重 | 审批链不驱动 BPMN | 多级审批是假功能 |
| 🟡 中等 | L2 ACL 层完全失效 | 安全层次降级 |
| 🟡 中等 | Ticket Type 垂直线死代码 | 维护负担 |
| 🟡 中等 | 双写 custom_field_values | 存储浪费 |
| 🟡 中等 | FieldDesigner 零引用 | 死代码 |

### 5.2 建议修复顺序

1. **统一角色体系**：将 `users.role` 枚举与 `roles` 表对齐，删除不可达角色或补全枚举
2. **补齐 permissions 表**：将 router 中使用的 20 个缺失 resource 全部加入 seeder，执行迁移
3. **补齐 ResourceActionMap**：所有新增路由路径加入映射
4. **修复 BPMN DTO**：将 snake_case 响应改为 camelCase DTO
5. **修复 BPMN 版本管理**：统一为一套版本方案，修复 `Atoi("1.0.0")` 和激活逻辑
6. **修复审批网关**：`evaluateCondition` 处理无条件多出线
7. **启用工单必填校验**：在 ticket create 路径调用 `RenderTemplate` 或等效校验
8. **把审批链接到 BPMN**：`ResolveForServiceRequest` 结果送入流程引擎
9. **清理死代码**：Ticket Type、FieldDesigner、GatewayEngine、nitram509 依赖

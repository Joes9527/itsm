# 服务目录自定义字段统一设计

## 问题背景

`docs/superpowers/plans/2026-08-05-dynamic-custom-fields.md` 上一轮实施把工单模板的自定义字段迁移到了共享的 `field_definitions`/`field_values` 子系统（Task 3/4），并把 Task 7 的目标定为"服务目录项接入同一套子系统"。但手工测试 + 代码审查发现 Task 7 选错了落点：

- `service.ServiceCatalogItemService`（Task 7 加了 field_definitions 支持的那个 service）在 `router.go` 里**没有注册任何 HTTP 路由**，没有 controller 文件——它只能被 Go 单元测试直接调用，前端完全调不到。
- 代码库里存在**两套独立的"服务目录"实现**，共用 `service_catalogs`/`service_catalog_items` 这组表名但语义、路由、UI 完全不同：
  - `handlers/service_catalog/`（域切片风格，`CLAUDE.md` 里定义的"newer style"）：`ServiceCatalog` 被当成"单个服务"本身，真实路由挂在 `/api/v1/service-catalogs`，管理员 UI 在 `/admin/service-catalogs`，员工侧浏览/申请在 `/service-catalog`——**这是真正在用的实现**。
  - `service/service_catalog_service.go` + `service/service_catalog_item_service.go`（水平分层风格，"legacy"）：`ServiceCatalog` 是目录/分类，`ServiceCatalogItem` 是目录下的具体项，**没有任何路由挂载**，纯孤立代码。`service/service_request_service.go`（同样是没有路由的 legacy 实现）会直接读 `ServiceCatalogItem` 表，但没有任何写入口——这条数据链路本身也是死的。

这正好撞上 `AGENTS.md` 的硬性规则："`handlers/<domain>/` 是新的领域分片风格；`controller/`+`service/` 是旧的水平分层风格……不要在两个地方实现同一个领域端点。"这条规则在 Task 7 之前就已经被违反，Task 7 在孤立分支上又叠了一层 field_definitions，让问题更隐蔽。

本设计的目标是把自定义字段能力从死代码路径迁移到真正有路由、有 UI 的实现上，并顺手废弃孤立的 `ServiceCatalogItem` 分支。

## 目标

- `handlers/service_catalog` 的 `ServiceCatalog`（管理员在 `/admin/service-catalogs` 创建/编辑的那个实体）能定义自定义字段。
- 员工在 `/service-catalog` 侧申请服务（`handlers/service_request` 的 `ServiceRequest`）时，能看到并填写这些自定义字段，值随请求一起落库、在详情页展示。
- 复用已有的 `field_definitions`/`field_values` 表和 `FieldDefinitionService`/`FieldValueService`，不新建表、不新建 Ent schema。
- 废弃 `ServiceCatalogItem` 及配套的孤立 legacy service 文件。

## 非目标（本次不做）

- `ServiceRequest.form_data` 里承载的系统已知字段（`title`/`reason`/`cost_center`/`data_classification`/`source_ip_whitelist`/`expire_at`/`compliance_ack`，见 `handlers/service_request/handler.go:431-473`）的拆分——继续保留在 `form_data`，本次只收编"字段定义里明确声明过的动态字段"这部分到 `field_values`，两者并存。这是延续上一版设计文档的既有边界，不重新评估。
- CMDB CI 类型扩展字段接入。
- 真正的拖拽式表单设计器（`field_definitions.config` 继续预留占位，不实现）。
- 工单模板自定义字段编辑 UI、`/tickets/templates` 菜单入口——这两项是独立的子项目（子项目 A / C），各自单独过 spec/plan。

## 数据模型（复用，不新建表）

不新建 Ent schema。沿用 `field_definitions`/`field_values` 现有表结构，新增两个 `entity_type` 取值：

| entity_type | entity_id 指向 | 用途 |
|---|---|---|
| `service_catalog` | `handlers/service_catalog` 的 `ServiceCatalog.ID` | 字段**定义**（管理员配置） |
| `service_request` | `ServiceRequest.ID` | 字段**值**（员工提交） |

`ticket_template`（工单模板，已有）与这两个新值互不冲突，`field_definitions` 的唯一约束是 `(tenant_id, entity_type, entity_id, name)`，不同 `entity_type` 天然隔离。

**多态关联先例**：这个写法沿用的是 `ent/schema/workflowinstance.go:31,34`（`entity_id int` + `entity_type string` 的组合）。全仓库范围内只有这一处是真正的 `entity_type+entity_id(int)` 先例——`ent/schema/approvalchain.go` 虽然也有 `entity_type` 字段（21行），但没有配套的 `entity_id`，它表示的是"这条审批链适用于哪一类实体"，不是指向具体某个实体实例，不构成同类先例。（上一版设计文档同时引用了这两处，其中 `approvalchain.go` 那部分引用是错的，本次改正。）

**事务边界（明确要求，非可选项）**：`FieldDefinitionService.ReplaceDefinitions` 采用"删除重建"策略——保存 `ServiceCatalog` 时，把该 `entity_type="service_catalog"`+`entity_id` 下所有 `field_definitions` 删除后按提交内容重新插入。**这个删除+重建必须在同一个 Ent 事务（`client.Tx(ctx)`）内完成**：如果中途失败（字段名撞了唯一约束、进程崩溃等），没有事务的话会出现"旧定义已删、新定义只插入一半"的静默数据丢失，违反 `CLAUDE.md` "事务边界放在 service/domain 层"的硬性要求。

这个函数在工单模板那次实施中已经按这个要求实现了（`itsm-backend/service/field_definition_service.go:36-84`，`client.Tx(ctx)` 包住删除+插入，`TestFieldDefinitionService_ReplaceDefinitions_TransactionRollback` 用真实唯一约束冲突验证回滚），本次直接复用同一个函数，不需要重新实现，但作为设计文档必须显式声明这个前提，不能只靠"实施时顺带做对了"。

`FieldValueService.CreateValues` 自身的事务只包住它内部**多条 field_values 记录之间**的写入（防止一次提交里几个字段值只插一半），不包含调用方的主记录创建。`ServiceRequest` 创建时写入 `field_values` 沿用工单那边已经验证过的既定模式（`itsm-backend/service/ticket_service.go` `CreateTicket`）：**先提交 `ServiceRequest` 主记录，成功后再单独调用一次 `CreateValues`；`CreateValues` 失败只记 `Warnw` 日志、不回滚已经创建的 `ServiceRequest`**——跟 SLA 计算失败的处理方式一致，字段值写入失败不应该阻塞主业务操作。这是已有代码里唯一经过验证的组合方式，本次直接照搬，不引入"整体回滚"这种没有先例、没有测过的新语义。

## API 集成

### 定义管理（`/admin/service-catalogs` 侧）

- `handlers/service_catalog/entity.go`：`ServiceCatalog` struct 新增 `Fields []service.FieldDefinitionInput`（复用 `service` 包已有的类型，不重新定义一份）。`FieldDefinitionService`/`FieldValueService` 从设计之初就是跨领域共享的基础设施（工单模板、服务目录共用同一套），不是某个领域私有的 `repository_impl`，`handlers/service_catalog` 引用它不违反 `CLAUDE.md` "不要跨 `handlers/<domain>` 调用其他领域内部实现"的边界规则——那条规则约束的是领域之间互相调用彼此的私有实现，不约束对共享基础设施的依赖。
- `dto.CreateServiceCatalogRequest`/`UpdateServiceCatalogRequest`（`dto/service_dto.go:231-246`）新增 `Fields []map[string]interface{} json:"fields,omitempty"`，形状跟工单模板一致：`{name,label,type,required,options}`。
- `handlers/service_catalog/service.go` 的 `Create`/`Update`：写完 `ServiceCatalog` 主记录后，调 `FieldDefinitionService.ReplaceDefinitions(ctx, tenantID, "service_catalog", catalog.ID, fields)`。
- `handlers/service_catalog/service.go` 的 `Get`/`List`：`Get`（单条，详情/编辑回显）调 `ListDefinitions`；`List`（列表）改成 `ListDefinitionsForEntities` 批量加载后按 `catalog.ID` 分组拼回响应——不逐条查询，避免重蹈 `ServiceCatalogItemService.ListServiceCatalogItems` 已经踩过的 N+1（该问题已在工单模板那次审查中发现并修复，这次直接照做，不需要重新发现一遍）。

### 值的提交（`ServiceRequest` 创建）

- `handlers/service_request/service.go` 的 `Create`：`s.repo.Create(ctx, newReq, approvals)` 成功拿到 `created.ID` 之后（不是同一事务，见下方"事务边界修正"），从 `reqData.FormData` 里取出**不在** `handler.go:431-473` 系统已知字段清单（`title`/`reason`/`cost_center`/`data_classification`/`source_ip_whitelist`/`expire_at`/`compliance_ack`）里的键，调 `FieldValueService.CreateValues(ctx, tenantID, "service_catalog", catalogID, "service_request", created.ID, values)`；失败只记 `Warnw`，不影响已创建的 `ServiceRequest` 返回给调用方。
- `handlers/service_request` 的详情响应 DTO 新增 `customFields`（复用工单 `CustomFieldValueResponse` 的形状：`{name,label,value}`），详情接口（单条）查一次 `field_values`；列表接口不查，维持跟工单一致的"列表不承载自定义字段，避免 N+1"设计。
- `GET /api/v1/service-catalogs/:id` 响应体的 `fields` 字段供员工侧提交表单读取渲染（复用定义管理里已经加的字段，不需要单独开新端点）。

### 前端改动点

- 抽一个共享的"自定义字段编辑器"组件（`Form.List` 动态增删 name/label/type/required/options），工单模板编辑页和 `/admin/service-catalogs` 编辑页共用，不各自复制一份 Ant Design 表单代码。
- `src/app/(main)/admin/service-catalogs/page.tsx`：编辑弹窗接入上面的共享组件。
- `src/app/(main)/service-catalog/request/[id]/page.tsx`：提交表单读取该服务的 `fields`，按 `field.type` 动态渲染输入项（复用工单创建页 `src/app/(main)/tickets/create/page.tsx` 已有的渲染逻辑：`textarea`/`select`/`number`/`date`，其余落到默认文本输入），提交时把值合并进 `formData`。
- `src/app/(main)/service-requests/[id]/page.tsx`：展示 `customFields`（各自一行，用 `label` 不用 `name`，跟工单详情页的展示方式一致）。

## 废弃 ServiceCatalogItem

确认以下文件在 `router.go`/`internal/bootstrap/app.go` 里没有任何路由/依赖注入引用后（已核实：搜索 `ServiceCatalogItemController`、`NewServiceCatalogItemService`、`service_catalog_item` 相关路由字符串均为零匹配；`service.NewServiceRequestService` 虽然在 `internal/bootstrap/app.go:381` 被实例化，但没有对应的 controller/路由消费它），整体删除：

- `itsm-backend/service/service_catalog_item_service.go`（含上一版加的 N+1 修复——该修复从未被真实调用过，随文件一起删）
- `itsm-backend/service/service_catalog_service.go`（legacy，与 `handlers/service_catalog` 重复）
- `itsm-backend/service/service_request_service.go`（legacy，与 `handlers/service_request` 重复）
- `itsm-backend/ent/schema/service_catalog_item.go`
- `ServiceCatalog`（Ent schema）上跟 `ServiceCatalogItem` 相关的 `edge.To("items", ...)`

**旧列/旧表清理不会自动发生，需要手写迁移文件**：`main.go` 的默认启动路径不调用 `client.Schema.Create`；`ITSM_BOOTSTRAP_ONLY=true` 那条路径（`internal/bootstrap/app.go:815` `InitializeStorage`）会调用它，但 Ent 的 `Schema.Create` 是**只增不删**——能把新表/新列建出来（已验证：本地用这条路径在 `itsm-postgres-dev` 上建出过 `field_definitions`/`field_values`），但**不会删除任何已有列或表**。`-tags migrate`（`cmd/migrate/main.go`）会删，但它是先 `DROP DATABASE` 再 `CREATE DATABASE` 的破坏性全量重建，不能用在有数据的库上。

这个项目真正的增量迁移路径是 `itsm-backend/migrations/*.sql` 手写 SQL + `migration/migrator.go`（`CLAUDE.md` 已确认这个约定）。本次删除 `service_catalog_items` 表以及 `ServiceCatalog` 上任何残留的引用，必须补一份 `itsm-backend/migrations/xxxxxxxx_drop_service_catalog_item.sql`，否则这次清理只在 Ent 生成代码层面"删除"了，真实 Postgres 库里表还在。

（备注，非本次决策依据，仅记录：`ServiceCatalog.form_schema`——父级那一列——在现有业务代码里从未被写入过，只有 `ServiceCatalogItem.form_schema` 被 `SetFormSchema` 调用过。这解释了为什么两版设计都没有给 `ServiceCatalog` 单独设计过字段存储：它以前确实没有承载过这个语义，本次是第一次让它承载。）

## 测试计划

- **单元测试**（`handlers/service_catalog`）：`Create`/`Update` 正确调用 `ReplaceDefinitions`；`List` 走批量加载、不产生 N+1（可以照抄工单模板那次加的 `TestServiceCatalogItemService_List_BatchLoadsFieldDefinitionsPerItem` 模式）；**跨租户隔离**——租户 A 的 `ServiceCatalog` 查不到租户 B 定义的字段，即使 `entity_id` 恰好撞上同一个数字。这条必须在设计文档里显式列出，不能只靠实施计划补——`field_definitions`/`field_values` 的每条查询都是手写 `Where(tenantID)`，代码库里没有全局拦截器强制这件事（`database/softdelete.go` 只处理软删除，不处理租户过滤），漏传一次 `tenantID` 就是跨租户数据泄露，`CLAUDE.md` 把这类问题列为必须 fail closed、必须加测试的硬性规则。
- **单元测试**（`handlers/service_request`）：`Create` 时 `field_values` 正确落库（覆盖有/无自定义字段两种路径）；`form_data` 里的系统已知字段不受影响、不被误收编成动态字段。
- **集成测试**（真实 Gin router）：`POST /service-catalogs` 建服务带字段 → `POST /service-requests` 提交字段值 → `GET /service-requests/:id` 断言 `customFields` 数组；列表接口不带 `customFields`。
- **前端**：`tsc --noEmit` + 手动验证：管理员在 `/admin/service-catalogs` 加字段、员工在 `/service-catalog` 申请时看到输入框、`/service-requests/:id` 详情页正确展示。

## 实施阶段建议

1. 后端：`handlers/service_catalog` 接入 `FieldDefinitionService`（定义的增删改查 + List 批量加载），补跨租户隔离测试。
2. 后端：`handlers/service_request` 的 `Create` 接入 `FieldValueService` 写值，详情接口接入读值。
3. 前端：抽共享自定义字段编辑器组件；接入 `/admin/service-catalogs` 编辑页、`/service-catalog` 申请页、`/service-requests/:id` 详情页。
4. 端到端集成测试。
5. 废弃 `ServiceCatalogItem`：删除孤立的 legacy service/schema 文件 + 补写 `migrations/*.sql` 迁移文件（删表/清理残留引用）+ 跑一遍全量测试确认没有隐藏依赖。

具体任务拆分交给下一步的实施计划。

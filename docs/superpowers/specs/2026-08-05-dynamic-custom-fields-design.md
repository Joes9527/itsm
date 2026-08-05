# 动态自定义字段子系统设计

## 问题背景

在实现"工单自定义字段结构化存储"过程中，发现代码库里已经存在 **5 处相互独立、互不相通**的"动态字段"相关实现：

| 位置 | 存的是什么 | 形状 |
|---|---|---|
| `TicketTemplate.form_fields` | 工单模板的字段**定义** | JSON `[]byte`，手动 `json.Unmarshal` |
| `TicketType.custom_fields` | 工单类型的字段**定义**（与上面功能重叠） | JSON `map[string]interface{}` |
| `ServiceCatalog.form_schema` / `ServiceCatalogItem.form_schema` | 服务目录的表单字段**定义** | JSON |
| `ServiceRequest.form_data` | 服务请求提交的表单**值** | JSON `map[string]any` |
| `Ticket.custom_field_values`（本次改动前一版加的） | 工单提交的自定义字段**值** | JSON `map[string]interface{}` |

用户在 UI 上验证工单自定义字段功能时，因为 (1) 前端 `getTemplates()` 一个历史 bug（响应字段名 `templates` vs 前端期望的 `items`，导致自定义模板在创建工单页面根本不可选）和 (2) 详情页把自定义字段值包在一个笼统的"自定义字段"字段下、且用原始 key 而非中文 label 展示，两个问题共同暴露出：自定义字段不应该是工单一个领域的局部方案，而应该做成平台级的通用能力，工单和服务目录（进而服务请求）先接入，CMDB 以后再接。

## 目标

- 一套统一的字段定义 + 字段值存储，工单模板、服务目录项共用同一套 API/Schema。
- 字段值展示时能拿到正确的 label 和显示顺序，且不依赖字段定义之后是否还存在（历史准确性）。
- 复用代码库已有的多态关联写法（`entity_type`+`entity_id`，见 `workflowinstance.go`/`approvalchain.go`）和已有的 JSON 存储惯例，不引入新的自造模式（不做分列式 EAV，不引入外部库）。

## 非目标（本次不做）

- CMDB CI 类型扩展字段接入——留到以后。
- 真正的拖拽式表单设计器——`field_definitions.config` 预留一个 JSON 列给以后的校验规则/默认值/显隐条件占位，但 v1 不实现这些能力。
- 字段值列表页展示、按值筛选/排序——只做详情页展示；`field_values.value` 上的索引策略预留但本次不建。
- 历史数据迁移——现有测试数据可以直接作废，无需保留。
- **`ServiceRequest.form_data` 的收编**——写实施计划时发现 `form_data` 并非单纯的"动态字段值"存储，而是同时承载了系统级已知字段（`title`/`reason`/`cost_center`/`data_classification`/`source_ip_whitelist`/`expire_at`/`compliance_ack`，见 `handlers/service_request/handler.go:431-473`，这些值会被摘出来写入 `ServiceRequest` 自己的专用列）和服务目录项 `form_schema` 定义的真动态字段。精确剥离两者风险较高，本次不动 `form_data`，服务目录侧的 `field_values`（entity_type="service_request"）只承接 `form_data` 系统已知 key 之外的真动态字段，与 `form_data` 并存。真要收编 `form_data` 需要单独立项。

## 数据模型

### `field_definitions`（字段定义，谁拥有这个字段）

```go
field.Int("tenant_id").Positive()
field.String("entity_type")   // "ticket_template" | "service_catalog_item"
field.Int("entity_id")        // 模板ID 或 服务目录项ID
field.String("name")          // 字段 key，如 office_location
field.String("label")         // 显示名，如 办公地点
field.String("field_type")    // text | textarea | number | date | select | ...
field.Bool("required").Default(false)
field.JSON("options", []interface{}{}).Optional()   // select/radio 的选项
field.Int("sort_order").Default(0)
field.JSON("config", map[string]interface{}{}).Optional() // 预留：校验规则/默认值/显隐条件
field.Bool("is_active").Default(true)
field.Time("created_at")
field.Time("updated_at")
```

索引：`(tenant_id, entity_type, entity_id, sort_order)`，`(tenant_id, entity_type, entity_id, name)` 唯一约束（同一模板/目录项下字段名不能重复）。

### `field_values`（提交的值，挂在具体的工单/服务请求实例上）

```go
field.Int("tenant_id").Positive()
field.String("entity_type")          // "ticket" | "service_request"
field.Int("entity_id")               // 工单ID 或 服务请求ID
field.Int("field_definition_id").Optional().Nillable()  // 指回 field_definitions，可空
field.String("field_name")           // 提交时快照
field.String("field_label")          // 提交时快照
field.Int("sort_order")              // 提交时快照
field.JSON("value", nil).Optional()  // 原始类型直接存：数字是 JSON number，日期是 ISO 字符串等
field.Time("created_at")
```

索引：`(tenant_id, entity_type, entity_id)`（主查询路径：取某个工单/服务请求的所有字段值）。

**关键设计决策**：

1. `field_values` 冗余快照 `field_name`/`field_label`/`sort_order`，读取时不需要反查 `field_definitions`——模板字段被改名/删除不影响历史工单的展示。`field_definition_id` 外键仅用于未来可能的追溯查询，不是渲染路径的依赖。
2. `value` 用单一 JSON 列而非分列式 EAV（不拆 value_text/value_number/value_date...）。理由：拆列本质是手写一套类型路由的读写逻辑，而 Postgres JSONB + Ent `field.JSON` 是这个代码库里已经反复验证过的现成方案（`TicketTemplate.form_fields` 等 5 处都是这么存的），维护成本最低。当前唯一高频查询路径（按 entity 取全部字段值）用普通 B-tree 索引就够，不需要为按值过滤优化；真有这个需求时，Postgres 对 JSONB 的 GIN 索引（`jsonb_path_ops`）或针对单个高频字段的表达式索引是成熟方案，届时按需加索引即可，不需要现在改表结构。
3. `field_definitions.options` 保持 JSON 而不拆成独立的选项表——选项只会整体编辑、整体展示，没有单独查询/关联单个选项的需求，拆表是过度设计。

## API 集成

### 定义管理（模板 / 服务目录项的字段配置）

`POST/PUT /tickets/templates`、服务目录项对应的创建/更新接口，请求体形状不变（`fields: [{name,label,type,required,options}]`），service 层从"写 JSON 列"改成"写 `field_definitions` 表"。

更新策略：**删除重建**——保存模板/目录项时，把该 `entity_type+entity_id` 下所有 `field_definitions` 删除后按提交内容重新插入，不做逐字段 diff。这个简化是安全的，因为 `field_values` 已经快照了 name/label/顺序，不依赖 `field_definitions` 行的存续。

读取：`GET /tickets/templates/:id` 等响应形状不变（`fields: [...]`），意味着已经建好的模板管理界面（`itsm-frontend/src/app/(main)/tickets/templates/page.tsx`）**不需要改动**，只是它读写的后端存储从 JSON 列换成了表。

### 值的提交（工单 / 服务请求创建）

沿用现有的 `formFields.values`（`{fieldName: value}`）提交方式。service 层拿到 `templateId`（或服务目录项 ID）+ 提交的值，查出对应 `field_definitions`（按 `sort_order` 排序），逐个生成 `field_values` 行（快照 name/label/sort_order，写入 value），与工单/服务请求创建在同一事务内完成。

本次改动会**撤掉**上一版加的 `Ticket.custom_field_values` 列及其配套的领域模型/repository/service/DTO 改动，改为统一走这套子系统。

### 值的读取

工单/服务请求的**详情**接口按 `(tenant_id, entity_type, entity_id)` 查一次 `field_values`，拼成有序数组返回：

```json
"customFields": [
  { "name": "office_location", "label": "办公地点", "value": "北京" },
  { "name": "device_count", "label": "设备数量", "value": 2 }
]
```

**列表**接口不解析 `customFields`，避免逐行查询造成 N+1；列表页展示自定义字段列的需求出现后再单独评估。

### 前端改动点

- `itsm-frontend/src/lib/api/ticket-api.ts` 的 `getTemplates()`：修正响应字段名不匹配的历史 bug（后端返回 `templates`，类型声明和调用方读的是 `items`），顺带修 `itsm-frontend/src/app/(main)/tickets/templates/page.tsx` 同样的读取点。
- `itsm-frontend/src/app/(main)/tickets/create/page.tsx`：现有的按 `field.type` 动态渲染逻辑不变，修好 `getTemplates()` 之后自动能选中自定义模板并看到输入框。
- `itsm-frontend/src/components/ticket/TicketDetail.tsx`：`ticket.customFields` 类型从 `Record<string, unknown>` 改为 `Array<{name; label; value}>`，渲染改成每个字段各自一个 `Descriptions.Item`（用 `label` 而非 `name`），不再包一层"自定义字段"外层。

## 清理范围

删除以下列（不保留、不做迁移，因为不需要考虑历史数据）：

- `TicketTemplate.form_fields`
- `TicketType.custom_fields`
- `ServiceCatalog.form_schema` / `ServiceCatalogItem.form_schema`
- `Ticket.custom_field_values`（本次改动前一版加的，随本设计一并撤销）

`ServiceRequest.form_data` **保留不动**——见上方"非目标"一节的说明，它同时承载系统级已知字段和真动态字段，本次不拆分，`field_values`（entity_type="service_request"）与它并存。

新增 Ent Schema：`field_definition.go`、`field_value.go`。

## 顺手偿还的技术债

实现"工单自定义字段结构化存储"时发现 `controller/ticket_controller.go`、`service/ticket_service.go`、`dto/mappers.go` 各自独立维护一份 Ticket→Response 转换逻辑，三处不同步导致新字段漏加了两处，靠手工 curl 才发现。本次改动会把 `customFields` 的解析逻辑收敛成一处，其余两处改为调用它，不再各自维护。

## 测试计划

- **单元测试**：`field_definitions`/`field_values` 的 repository 层 CRUD；工单/服务请求创建时字段值正确落库（覆盖有/无自定义字段两种路径）；模板/目录项更新时"删除重建"策略正确。
- **集成测试**：过真实 Gin router 的端到端测试（`POST /tickets/templates` 建模板 → `POST /tickets` 提交字段值 → `GET /tickets/:id` 断言 `customFields` 数组），落在 `itsm-backend/tests/integration/`，避免重蹈"改了一处 mapper、另外两处没同步、只能靠手工 curl 发现"的覆辖，这类问题以后由 CI 自动抓到。
- **前端**：`tsc --noEmit` + 手动浏览器验证创建工单页面能看到自定义字段输入框、详情页字段各自独立展示、label 正确。

## 实施阶段建议

范围较大（新增 2 张表、改 2 个领域、删 5 个旧列、touch 前后端多处），建议实施计划按以下顺序分阶段：

1. 新增 `field_definitions`/`field_values` Ent Schema + repository 层，工单模板先接入（定义管理 + 值提交/读取），撤销 `Ticket.custom_field_values` 旧实现。
2. 服务目录项接入同一套子系统（定义管理 + 服务请求的值提交/读取）。
3. 前端：修 `getTemplates()` bug、`TicketDetail.tsx` 展示改造。
4. 清理 5 处旧 JSON 列 + 收敛 3 处 shadow mapper。
5. 补集成测试。

具体任务拆分交给下一步的实施计划。

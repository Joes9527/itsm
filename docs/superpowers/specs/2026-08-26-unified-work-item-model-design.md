# 统一 Work Item 的标准 ITSM / ServiceNow 式领域模型设计

> 状态：Proposed  
> 日期：2026-08-26  
> 适用范围：Ticket、Service Catalog、Service Request、Incident、Problem、Known Error、Change、履约任务及其共享能力

## 1. 摘要

当前系统已经具备 Ticket、Service Request、Incident、Problem、Known Error、Change、BPMN、SLA、动态字段、RBAC 和审计等基础能力，但领域模型仍是“通用 Ticket 与多个独立专业实体并存”：

- Service Request 已通过 `ticket_id` 委托 Ticket 承担状态、审批和流程；
- Incident、Problem、Change 仍各自重复保存标题、状态、优先级、处理人、租户和时间戳等公共字段；
- 服务目录中的 Incident 路径会直接创建 Incident，绕过 Ticket；
- 跨域关系分散在 Ent Edge、JSON 字段和领域接口中；
- 评论、附件、SLA、审批、审计、通知和事件总线尚未完全围绕同一基础记录收敛。

本设计将现有 `Ticket` 演进为领域层的 `WorkItem`（统一工作记录），所有核心 ITSM 对象共享该基础记录，并通过一对一扩展表保存专业字段：

```text
WorkItem
├── ServiceRequestItem
├── Incident
├── Problem
├── ChangeRequest
└── CatalogTask
```

统一的是记录身份、协作能力和横切能力，不是专业生命周期。Incident、Problem、Change 和 Service Request 仍由各自领域服务验证状态转换、权限和业务规则。

第一阶段不物理重命名 `tickets` 表，避免无业务价值的大范围迁移；代码中的领域接口和文档使用 `WorkItem`，数据库继续以 `tickets` 作为基础表。待全部专业域完成迁移后，再单独评估是否需要物理改名。

## 2. 目标与成功标准

### 2.1 目标

1. 每个 Service Request Item、Incident、Problem、Change Request 和 Catalog Task 必须对应且仅对应一条 WorkItem。
2. WorkItem 成为编号、标题、描述、分派、状态、优先级、SLA、流程、评论、附件、时间线、审计和通知的统一记录身份。
3. 专业扩展实体仅保存该领域独有的数据，不再复制 WorkItem 公共字段。
4. 服务目录负责“用户申请什么”，Ticket Category 负责“运营如何分类和处理”，Ticket Template 负责“坐席或内部任务如何标准化录入”。
5. Incident、Problem、Change 和 Service Request 之间通过显式、租户隔离、可审计的关系模型协作。
6. 保留现有领域 API 和专业页面，不新增第二套平行路由或兼容服务。
7. 所有迁移都能验证租户隔离、权限、审计、流程和历史数据完整性。

### 2.2 成功标准

- 任意专业记录都能通过一个稳定的 WorkItem ID 查询公共信息和完整时间线。
- 任意 WorkItem 最多关联一个同类型专业扩展记录。
- 不能创建没有 WorkItem 的 Incident、Problem、Change、Requested Item 或 Catalog Task。
- Incident → Problem、Incident/Problem → Change、Requested Item → Change/Catalog Task 均有显式关系和审计记录。
- 服务目录创建 Incident 时不再绕过 WorkItem。
- 新建、列表、详情、状态变更、评论、附件、SLA、流程、审计、通知均通过真实 HTTP 主链路验证。
- 迁移后不存在继续写入旧公共字段或旧 JSON 关系字段的代码路径。

## 3. 非目标

本设计不做以下事项：

- 不把所有专业状态机合并成一个通用状态机；
- 不把 Incident、Problem、Change、Service Request 合并成单张业务表；
- 不用前端路由或菜单隐藏替代后端授权；
- 不在本次模型设计中重写 BPMN 引擎；
- 不同时建设新的 API 版本并长期保留旧 API；
- 不立即引入服务目录购物车、定价结算或复杂订单能力；
- 不把 Known Error 合并为 WorkItem。Known Error 是知识性记录，可关联 Problem，但不是待办工作记录；
- 不把 Service Catalog Item 合并为 WorkItem。Catalog Item 是可申请产品定义，不是一次工作执行实例。

## 4. 术语与正式定义

| 术语 | 正式定义 | 是否为 WorkItem |
|---|---|---|
| WorkItem / Task | 所有可分派、可跟踪、可审计 ITSM 工作的统一基础记录 | 是，基础类 |
| Ticket / 工单 | 产品层对 WorkItem 的通俗名称，不再代表某个独立专业流程 | 同 WorkItem |
| Intake / Interaction | 尚未完成分诊的用户交互或接入记录 | 可选，见第 9 节 |
| Service Catalog | 面向用户展示的服务集合 | 否 |
| Catalog Group / Service Domain | 门户浏览和服务能力分组 | 否 |
| Catalog Item / Offering | 可申请的标准服务定义、表单、流程和履约策略 | 否 |
| Request | 一次用户提交的请求头，可包含多个 Requested Item | 可选作为 WorkItem |
| Requested Item / ServiceRequestItem | 一个具体目录项的一次申请实例 | 是 |
| Catalog Task | Requested Item 拆出的审批、履约或交付任务 | 是 |
| Incident | 为尽快恢复服务而管理的未计划中断或质量下降 | 是 |
| Problem | 为识别和消除一个或多个 Incident 根因而管理的记录 | 是 |
| Known Error | 已分析且具有已知根因或 Workaround 的知识性记录 | 否 |
| Change Request | 对服务、应用、基础设施或配置进行受控变更的记录 | 是 |
| Ticket Category | 运营侧分类树，用于路由、SLA、报表、知识和处理组 | 否 |
| Ticket Template | 坐席或内部工作快速录入模板 | 否 |

### 4.1 命名决策

- 前端产品文案可继续使用“工单”。
- 后端领域层新增共享接口时使用 `WorkItem`，避免继续扩大 `Ticket` 的双重含义。
- 现有 `/api/v1/tickets` 保留为通用工作视图。
- 现有 `/api/v1/incidents`、`/problems`、`/changes`、`/service-requests` 保留为专业领域 API。
- API JSON 字段统一使用 camelCase。

## 5. 设计原则

### 5.1 共享基类，专业生命周期

WorkItem 管理共享字段与共享行为；专业域管理专业规则：

- WorkItem 可提供统一分派、评论、附件、关注人、SLA、流程引用、活动时间线和审计；
- IncidentService 验证 acknowledge、resolve、close、major incident 等事件规则；
- ProblemService 验证 investigate、root cause、known error、resolve、close；
- ChangeService 验证 assess、authorize、schedule、implement、review、rollback、close；
- ServiceRequestService 验证目录项、审批、履约和交付。

禁止新增一个通过巨大 `switch recordClass` 实现所有生命周期的通用服务。

### 5.2 一个权威字段只有一个写入位置

- 标题、描述、状态、优先级、请求人、处理人、处理组、分类、租户、版本和公共时间戳以 WorkItem 为权威来源；
- 专业扩展表不能继续保存同名快照字段；
- 专业列表所需的公共字段通过联查或批量查询组装 DTO；
- 不用缓存重复查询来掩盖重复数据模型。

### 5.3 关系不是状态

Incident 不能通过修改 `type=problem` 变为 Problem；Problem 也不能通过修改类型变为 Change。跨域协作通过新建目标记录和关系实现，源记录继续保留其历史和生命周期。

### 5.4 租户、权限和审计默认关闭式

- 所有 WorkItem、扩展记录和关系必须包含或可结构化约束到同一 tenant；
- 任一跨域关系写入前必须验证两端 tenant 一致；
- 读取扩展记录前先验证 WorkItem 和专业域权限；
- AI、流程、连接器和批量操作创建或变更 WorkItem 时必须记录 actor、来源和审计事件。

## 6. 目标领域模型

### 6.1 聚合关系

```text
                    ┌────────────────────┐
                    │ ServiceCatalogItem │
                    └─────────┬──────────┘
                              │ defines
                              ▼
┌───────────────┐    1:1   ┌───────────────────┐
│ RequestedItem │──────────▶│                   │
└───────────────┘           │                   │
                            │                   │
┌───────────────┐    1:1   │                   │
│   Incident    │──────────▶│     WorkItem      │
└───────────────┘           │   (tickets 表)     │
                            │                   │
┌───────────────┐    1:1   │                   │
│    Problem    │──────────▶│                   │
└───────────────┘           │                   │
                            │                   │
┌───────────────┐    1:1   │                   │
│ ChangeRequest │──────────▶│                   │
└───────────────┘           └─────────┬─────────┘
                                     │
┌───────────────┐    1:1             │ relates through
│  CatalogTask  │─────────────────────┘
└───────────────┘                     ▼
                           ┌──────────────────────┐
                           │  WorkItemRelation    │
                           └──────────────────────┘
```

### 6.2 WorkItem 基础表

第一阶段复用现有 `tickets` 表，目标字段如下：

| 字段 | 说明 |
|---|---|
| `id` | 内部主键 |
| `ticket_number` | 租户内稳定且不可变的最终业务编号，直接包含专业前缀；API 映射为 `number` 或保留 `ticketNumber` |
| `record_class` | `generic`、`service_request_item`、`incident`、`problem`、`change_request`、`catalog_task` |
| `title`, `description` | 公共内容 |
| `status` | 当前专业类的状态值，由专业服务校验 |
| `priority` | 统一优先级 |
| `source` | `manual`、`service_catalog`、`monitoring`、`email`、`connector`、`ai`、`api` |
| `requester_id` | 服务接受者或请求人 |
| `opened_by_id` | 实际录入或触发者 |
| `assignee_id` | 当前处理人 |
| `assignment_group_id` | 当前处理组 |
| `category_id` | TicketCategory |
| `parent_work_item_id` | 仅用于执行层级；复杂业务关系使用关系表 |
| `workflow_instance_id` | 当前权威流程实例引用 |
| `sla_definition_id` | 当前 SLA 策略引用 |
| SLA 时间字段 | 响应、解决、暂停、恢复等权威时间 |
| `version` | 乐观锁 |
| `created_at`, `updated_at`, `resolved_at`, `closed_at` | 公共生命周期时间 |
| `tenant_id` | 租户边界 |
| `deleted_at` | 软删除 |

`tickets.ticket_number` 直接保存最终展示和检索使用的专业业务编号，例如
`INC-202608-000001`、`PRB-202608-000001`、`CHG-202608-000001`、
`RITM-202608-000001` 或 `TKT-202608-000001`，不再额外维护一套只供底层使用的
WorkItem 全局编号。编号生成采用“租户 + recordClass + 时间分区”的原子序列，前缀来自可配置的
编号规则；Redis key 建议使用
`sequence:work_item:{tenantId}:{recordClass}:{yyyyMM}`，数据库回退路径必须使用同一分区语义。
数据库以 `(tenant_id, ticket_number)` 唯一索引作为最终并发防线，发生冲突时重新取号，不能通过
随机覆盖或忽略冲突继续创建。编号一经生成不可修改。

### 6.3 `recordClass` 约束

- 新建专业记录时，WorkItem 与扩展记录必须在同一事务内创建；
- `recordClass` 默认不可修改；
- `generic` 记录完成分诊时，允许在事务中一次性修改为目标类并创建扩展记录；
- 已创建专业扩展后禁止再次修改 `recordClass`；
- `recordClass` 与扩展表必须一致，后台完整性检查持续验证；
- `type` 字段在迁移后不再同时承担“专业类”和“业务子类型”两种职责。专业类由 `recordClass` 表示，业务子类型放入扩展表。

### 6.4 专业扩展表

#### Incident

保留：

- `work_item_id`，唯一、必填；
- `impact`、`urgency`、`severity`；
- `detected_at`；
- `configuration_item_id` 或受影响 CI 关系；
- `is_major_incident`；
- `escalation_level`、`escalated_at`；
- `resolution_code`、`resolution_steps`；
- `impact_analysis`、事件专属 metadata。

迁出或删除：

- title、description、status、priority；
- reporter、assignee；
- tenant、公共时间戳；
- 与 WorkItem 重复的 source 和 version。

#### Problem

保留：

- `work_item_id`，唯一、必填；
- `root_cause`；
- `workaround`；
- `resolution`；
- 问题调查状态或分析方法；
- Known Error 相关引用或派生状态。

迁出或删除：

- title、description、status、priority、category；
- assignee、created_by、tenant；
- 公共时间戳。

#### ChangeRequest

保留：

- `work_item_id`，唯一、必填；
- `change_type`：standard、normal、emergency；
- justification、risk、impact；
- implementation、test、rollback plan；
- planned/actual window；
- CAB/审批策略引用；
- implementation result、PIR 引用。

迁出或删除：

- title、description、status、priority；
- assignee、created_by、tenant；
- 公共时间戳；
- `related_tickets` JSON 权威字段。

#### ServiceRequestItem

现有 ServiceRequest 已接近目标模型，保留：

- 逻辑上的 `workItemId` 使用现有物理字段 `ticket_id`，唯一、必填；
- `catalog_item_id`（现有 `catalog_id`）；
- quantity、expected_at；
- contact、cost center、data classification、合规和资源交付专属字段；
- 目录提交的表单值引用；
- fulfillment 状态的派生信息。

不再维护独立标题、状态、审批和公共处理字段。

第一阶段不把 `service_requests.ticket_id` 重命名为 `work_item_id`。服务和 DTO 在领域语义中将其
解释为 `workItemId`，数据库继续保留已经运行且带唯一索引的 `ticket_id`，避免一次没有业务收益的
DDL、Ent 生成代码和查询迁移。只有在全部专业域完成统一、并决定物理重命名 `tickets` 表时，才统一
评估这些外键列是否改名。

#### CatalogTask

新增 CatalogTask 的触发条件不是“存在服务请求”，而是一个 Requested Item 需要拆分给多个团队、多个步骤或独立 SLA。字段包括：

- `work_item_id`，唯一、必填；
- `requested_item_id`；
- `task_type`：approval、fulfillment、validation、delivery；
- `sequence`；
- `fulfillment_definition_id`；
- 专业执行结果。

简单服务请求可以只用 Requested Item，不强制创建 Catalog Task。

## 7. 服务目录目标模型

### 7.1 职责边界

Catalog Item 负责：

- 面向用户的名称、说明、展示分组；
- 动态申请表单；
- 目标专业类；
- 默认运营分类；
- 默认优先级；
- 审批/流程绑定；
- 履约模板；
- SLA 策略；
- 是否允许自助、代申请和批量申请。

Catalog Item 不负责：

- 保存一次申请的运行状态；
- 保存实际处理人；
- 充当 Ticket Template；
- 直接替代 Incident、Change 或 Requested Item。

### 7.2 Catalog Item 新增或规范化字段

| 字段 | 用途 |
|---|---|
| `catalog_group_id` | 门户展示分组，替代无约束的分类字符串 |
| `target_class` | `service_request_item`、`incident` 或 `change_request` |
| `default_ticket_category_id` | 默认运营分类 |
| `default_priority` | 默认优先级 |
| `fulfillment_template_id` | 可选内部任务模板 |
| `process_definition_key` | 专属 BPMN 流程 |
| `sla_policy_id` | 服务承诺 |
| `request_mode` | self_service、agent_assisted、both |
| `status` | draft、published、retired |

`service_type` 保留用于业务表单和资源类型，不再承担 ITSM 专业类的含义；`itsm_type` 迁移为受约束的 `target_class`。

### 7.3 Request / Requested Item / Catalog Task

分阶段支持 ServiceNow 式请求层级：

1. 当前单项申请继续直接创建 Requested Item；
2. 当产品需要购物车或一次申请多个服务项时，增加 Request Header；
3. 每个目录项生成一个 Requested Item；
4. BPMN 或履约定义按需生成 Catalog Task；
5. Request 总状态由 Requested Item 聚合，不独立复制专业状态；
6. Requested Item 完成条件由所有必需 Catalog Task 和审批结果共同决定。

## 8. 工单分类与模板

### 8.1 TicketCategory

TicketCategory 是统一运营分类树，服务于：

- 分派和处理组；
- SLA 和升级；
- 报表与成本核算；
- 知识检索；
- 自动化与 AI 分类；
- 不同专业域的分类约束。

建议为分类增加 `applicableClasses`，明确该分类可用于 Incident、Problem、Change、Requested Item 或通用 WorkItem。创建和更新时由后端验证。

### 8.2 TicketTemplate

TicketTemplate 定位为内部快速录入和执行模板：

- 适用于坐席代建、批量建单、标准操作任务；
- 绑定 `applicableRecordClass` 和 TicketCategory；
- 提供默认值和动态字段；
- 不能替代 Catalog Item；
- 不能作为终端用户服务目录的权威来源。

当前 `category` 字符串应退化为展示信息或删除，权威关联使用 `categoryIds`。模板创建、更新 API 必须完整读写 `categoryIds`。

### 8.3 动态自定义字段归属

动态字段采用“定义来源与实例归属分离”的模型：

- `field_definitions` 继续归属于 TicketTemplate 或 Catalog Item，用于定义字段名、类型、必填、
  选项、排序和校验规则；
- 用户提交后生成的 `field_values` 统一归属于 WorkItem，即
  `entity_type=ticket, entity_id=workItemId`；
- 从 Catalog Item 提交的值仍使用 Catalog Item 的字段定义进行服务端校验，并在 FieldValue 中保留
  definition ID、name、label 和 sort order 快照；
- Incident、Problem、Change、Requested Item 的稳定、可查询、参与业务规则的强类型专业字段必须
  存入专业扩展表，不能伪装成动态字段；
- `ServiceRequest.form_data` 只保留尚未结构化的复合输入或流程上下文，不能与 `field_values`
  同时成为同一字段的权威来源；
- 通用详情页按 WorkItem ID 批量加载 FieldValue，专业 Panel 再读取扩展表字段。

这样既允许 Catalog Item 和 TicketTemplate 定义不同表单，也确保一次工作记录的动态值只有一个
实例归属，不会再次形成 Ticket、ServiceRequest 和专业扩展三套值存储。

## 9. Intake 与分诊

ServiceNow 有 Interaction 等接入概念。当前产品可先采用轻量方案：

- 邮件、门户“报告问题”、坐席人工录入、监控告警可创建 `recordClass=generic` WorkItem；
- 分诊在同一事务中确定 Incident 或 Requested Item，并创建对应扩展记录；
- 分诊前只允许有限状态：new、triaging、cancelled；
- 分诊后 `recordClass` 不可变；
- 记录分类建议、置信度、模型、提示版本、接受/拒绝结果和操作人；
- 若输入已经来自明确 Catalog Item 或 Incident API，则直接创建目标类，无需 generic 中间状态。

这样既支持统一入口，又避免为所有已知类型请求额外创建无意义的“母单 + 子单”两条业务记录。

## 10. 跨域关系模型

### 10.1 WorkItemRelation

新增结构化关系表：

| 字段 | 说明 |
|---|---|
| `id` | 主键 |
| `tenant_id` | 租户 |
| `source_work_item_id` | 源 WorkItem |
| `target_work_item_id` | 目标 WorkItem |
| `relation_type` | 关系类型 |
| `created_by` | 创建人 |
| `metadata` | 少量关系专属元数据，不存业务主体 |
| `created_at` | 创建时间 |
| `deleted_at` | 软删除 |

唯一约束：

```text
(tenant_id, source_work_item_id, target_work_item_id, relation_type)
```

禁止自关联，除非未来明确增加支持自引用的关系类型。

### 10.2 关系类型

| 关系 | 合法源 → 目标 | 含义 |
|---|---|---|
| `investigated_by` | Incident → Problem | Incident 由 Problem 调查根因 |
| `caused_by` | Incident → Problem | 已确认 Incident 由该 Problem 导致 |
| `resolved_by_change` | Incident/Problem → Change | 通过 Change 实施修复 |
| `requested_change` | Requested Item → Change | 服务申请触发受控变更 |
| `fulfilled_by` | Requested Item → Catalog Task | 履约任务 |
| `parent_child` | WorkItem → WorkItem | 任务拆分 |
| `duplicate_of` | 同类记录 → 同类记录 | 重复记录 |
| `related_to` | 任意允许类 | 无方向性的一般关联 |

Problem → Known Error 使用单独的领域关系，因为 Known Error 不是 WorkItem。

### 10.3 关系写入规则

- Controller 只绑定请求并调用领域服务；
- 领域服务验证 actor、tenant、两端类型和权限；
- 创建目标专业记录与关系应在同一事务；
- 关系创建和删除必须写审计事件；
- 读取时既校验关系所在 tenant，也校验目标记录可见性；
- 不开放可绕过领域约束的任意关系批量写接口。

## 11. 生命周期与状态

### 11.1 WorkItem 状态字段

状态物理存储在 WorkItem，但合法值由 `recordClass` 对应的专业服务定义。不能建立一个包含所有状态的全局无约束字符串集合。

### 11.2 建议状态集合

#### Generic

```text
new -> triaging -> classified
               └-> cancelled
```

`classified` 是瞬时审计结果；完成分类后记录进入专业类。

#### Incident

```text
new -> acknowledged -> in_progress -> resolved -> closed
                       ├-> pending
                       └-> cancelled
resolved -> in_progress  (reopen)
```

#### Problem

```text
new -> assessing -> investigating -> known_error -> resolved -> closed
                       ├-> pending
                       └-> cancelled
resolved -> investigating  (reopen)
```

#### Change Request

```text
draft -> assess -> authorize -> scheduled -> implement -> review -> closed
                    ├-> rejected
scheduled/implement  ├-> cancelled
implement            └-> rolled_back -> review
```

Standard Change 可复用预授权模型，但每次执行仍产生 Change WorkItem 和审计。

#### Requested Item

```text
new -> awaiting_approval -> approved -> fulfillment -> completed -> closed
                      └-> rejected
new/approved/fulfillment -> cancelled
```

### 11.3 状态变化副作用

专业服务完成状态验证后，在一个事务或可靠事件边界内：

1. 更新 WorkItem 状态和权威时间；
2. 更新专业扩展字段；
3. 写活动记录和审计；
4. 发布领域事件；
5. 驱动 SLA、通知、流程和关系联动；
6. 保持幂等，避免事件重试重复执行。

## 12. API 设计

### 12.1 路由原则

保留：

- `/api/v1/tickets`：统一工作视图、通用查询、评论、附件、关注、时间线；
- `/api/v1/incidents`：Incident 专业行为；
- `/api/v1/problems`：Problem 专业行为；
- `/api/v1/changes`：Change 专业行为；
- `/api/v1/service-requests`：Requested Item 专业行为；
- `/api/v1/service-catalogs`：Catalog Item 定义。

不新增 `/api/v2/work-items` 与旧路由长期并存。若需要通用代码入口，在内部 service/repository 层引入 WorkItem 接口，HTTP 合同保持稳定。

### 12.2 DTO 组装

- Controller 禁止直接返回 Ent 模型；
- 专业详情响应由 WorkItem DTO + 专业扩展 DTO 组成；
- 列表由服务层批量联查，禁止逐行 N+1；
- JSON 字段统一 camelCase；
- 响应中可提供 `recordClass` 和 `actions`；
- `actions` 由后端根据专业状态、RBAC 和关系计算，前端不自行推断授权。

专业列表必须使用统一批量加载规范：

1. 先按 tenant、recordClass、过滤条件和分页查询 WorkItem；
2. 收集本页 WorkItem ID，一次执行
   `WHERE work_item_id IN (...)` 查询专业扩展数据；
3. 动态字段、关系、用户和操作权限也按 ID 集合批量加载，只有响应明确需要时才查询；
4. Service 层使用 map 按 WorkItem ID 组装 DTO，Controller 不再补查；
5. 列表读取不使用长事务，不在持有数据库事务时调用外部服务；
6. 单一专业列表的基础组装预算为 WorkItem、专业扩展、必要辅助数据三个查询组；可选关系、动态
   字段等展开数据必须由显式参数控制并分别批量查询；
7. 为 `tickets(tenant_id, record_class, status, updated_at)` 和专业扩展
   `work_item_id` 建立匹配索引；
8. 分页上限和 p95 响应目标写入可配置的 API 性能预算并由集成/性能测试验证，不在 Service 中
   硬编码阈值。

共享层提供 WorkItem 批量加载器负责公共字段、用户、动态字段和操作权限投影；每个专业 Repository
提供自己的 `ListByWorkItemIDs` 批量方法。批量加载器只编排读查询和 DTO 投影，不包含专业业务规则，
也不通过一个跨全域的大型 JOIN 取代专业 Repository。

示例：

```json
{
  "id": 1001,
  "number": "INC-202608-000001",
  "recordClass": "incident",
  "title": "邮件服务不可用",
  "status": "in_progress",
  "priority": "critical",
  "requesterId": 21,
  "assigneeId": 42,
  "categoryId": 8,
  "incident": {
    "impact": "high",
    "urgency": "critical",
    "isMajorIncident": true,
    "detectedAt": "2026-08-26T10:00:00Z"
  },
  "actions": {
    "resolve": { "allowed": true },
    "createProblem": { "allowed": true },
    "createChange": { "allowed": false, "reason": "permissionDenied" }
  }
}
```

### 12.3 创建语义

- 专业创建接口在同一事务创建 WorkItem 和扩展记录；
- 公开接口不能信任客户端传入 tenant、requester、source、actor；
- Catalog Item 决定目标类、默认分类、模板和流程；
- AI 只能提出分类建议，除非明确配置自动化策略且通过权限、置信度和审计门槛；
- 幂等来源必须提供 idempotency key 或外部消息 ID。

## 13. 事务、错误处理与一致性

### 13.1 强一致边界

以下操作必须在同一数据库事务中：

- WorkItem + 专业扩展创建；
- generic 分诊为专业类；
- Incident 创建 Problem 并建立关系；
- Incident/Problem/Requested Item 创建 Change 并建立关系；
- 专业状态变更 + WorkItem 状态/时间更新；
- 关系写入 + 审计 outbox 记录。

### 13.2 外部副作用

通知、Webhook、连接器、搜索索引、向量库和外部配置执行不能放在数据库事务内。数据库事务写入 Outbox/Event，消费者幂等处理外部副作用。

### 13.3 失败策略

- 基础记录创建成功但扩展创建失败：事务整体回滚；
- 关系创建失败：目标专业记录与关系整体回滚；
- 审计事件无法持久化：高风险操作整体失败；
- 通知失败：业务事务不回滚，记录失败并支持重试；
- SLA 或流程绑定缺失：按产品配置决定阻断或使用明确的默认策略，禁止静默成功；
- 不使用 broad catch 或 success-shaped fallback。

## 14. 事件、审计与时间线

### 14.1 统一事件

至少定义：

- `work_item.created`
- `work_item.classified`
- `work_item.assigned`
- `work_item.status_changed`
- `work_item.related`
- `work_item.unrelated`
- `incident.major_declared`
- `problem.known_error_created`
- `change.authorized`
- `change.implemented`
- `change.rolled_back`
- `requested_item.fulfillment_started`
- `requested_item.completed`

事件载荷至少包含 tenantId、workItemId、recordClass、actor、correlationId、timestamp 和版本。

### 14.2 审计要求

审计记录必须覆盖：

- 创建、分类、分派、状态变化；
- 跨域记录创建和关系变化；
- 审批、CAB、回滚、批量操作；
- AI 建议及接受/拒绝；
- 连接器或流程自动动作；
- Catalog Item、分类、模板和流程绑定变更。

活动时间线面向业务用户，审计日志面向治理和合规，二者可以共享事件源但不能互相替代。

## 15. SLA、BPMN 与审批

### 15.1 SLA

- SLA 绑定 WorkItem；
- 策略选择可依据 recordClass、Catalog Item、TicketCategory、priority、service/CI；
- Incident 使用响应/恢复目标；
- Requested Item 使用审批/交付目标；
- Change 使用评估/授权/实施窗口目标；
- Problem 使用调查和根因目标；
- 暂停条件由专业状态机提供，计时器使用权威状态时间。

### 15.2 BPMN

- Process Instance 以 WorkItem ID 作为统一 business ID；
- `businessType` 使用稳定 recordClass，而不是控制器名称；
- Catalog Item 的 `processDefinitionKey` 可覆盖通用绑定；
- 专业服务发起流程，Controller 不直接拼装流程副作用；
- 不允许专业域另建审批引擎。

### 15.3 审批

- Requested Item、Change 和高风险 Catalog Task 均通过统一 BPMN Task/Approval 能力；
- Change 的 CAB 是 Change 专业流程中的审批形态，不新建 CAB 引擎；
- 审批结果写入统一任务和审计记录；
- 自审批、代理、会签、拒绝和撤回规则由统一授权服务约束。

## 16. 权限、租户与数据可见性

### 16.1 权限模型

保留专业资源：

- `ticket`
- `service_request`
- `incident`
- `problem`
- `change`
- `service_catalog`
- `ticket_template`
- `ticket_category`

通用 WorkItem 查询不能因为拥有 `ticket:read` 就自动读取全部专业记录。查询结果必须是“通用可见性 + 专业资源权限 + 行级范围”的交集。

### 16.2 租户约束

- WorkItem 必须有 tenantId；
- 扩展表可冗余 tenantId 以强化结构化隔离，但必须与 WorkItem 一致；
- WorkItemRelation 必须有 tenantId；
- 所有外键查询同时包含 tenant 条件；
- 后台任务、SLA、事件消费者和迁移程序都必须按 tenant 分区执行；
- 跨租户关系和关联创建一律失败。

### 16.3 MSP

MSP 操作者访问客户 tenant 时，actor tenant 与 data tenant 分开记录：

- `actorTenantId`
- `targetTenantId`
- `actorUserId`

授权由 MSP 关系和专业资源权限共同决定，审计中必须保留两层身份。

## 17. 前端信息架构

### 17.1 入口

- 终端用户：服务目录、报告故障、我的请求；
- 服务台坐席：统一新建、统一工作队列、待分诊；
- 专业人员：Incident、Problem、Change、Catalog Fulfillment 专业队列；
- 管理员：目录、分类、模板、流程、SLA 和权限配置。

### 17.2 `tickets/create`

重新定位为“坐席/人工建单”：

- 先选择目标类或由分诊建议；
- 选择 TicketCategory；
- 对非目录类记录可选择 TicketTemplate；
- 创建 Incident/Problem/Change 时调用对应专业 API，不再只创建带 `type` 字符串的普通 Ticket；
- 终端用户申请标准服务必须走 Service Catalog；
- 若选择 Catalog Item，转入目录申请表单而不是套用 TicketTemplate。

### 17.3 详情页

统一 WorkItem Shell 提供：

- 编号、标题、状态、优先级、请求人、分派；
- SLA、流程、评论、附件、时间线、关系；
- 后端计算的操作权限。

专业 Tab/Panel 提供 Incident、Problem、Change 或 Requested Item 字段。保留专业 URL，复用 Shell，不复制公共详情实现。

### 17.4 状态体验

所有页面必须覆盖 loading、empty、error、permission denied、conflict、success 和 stale version 状态。乐观锁冲突不能静默覆盖。

## 18. 数据迁移方案

### 18.1 总体原则

- 不新增平行业务路由；
- 逐域迁移，单个领域迁移必须包含 schema、数据回填、服务切换和旧写路径删除；
- 先双读验证可以用于离线校验，但生产业务代码不长期双写；
- 每阶段都提供可重复执行、按 tenant、可审计的迁移命令；
- 迁移前后记录数量、关系数量、字段校验和必须可对账。

### 18.2 Phase 0：模型冻结与基线

1. 形成并批准本设计；
2. 盘点 Ticket/Incident/Problem/Change/SR 数据量和孤儿记录；
3. 明确状态映射、编号冲突和重复公共字段优先级；
4. 修复阻断迁移的现有断链；
5. 为主链路补合同和集成测试；
6. 冻结新增独立公共字段和 JSON 关系。

### 18.3 Phase 1：WorkItem 基类和关系基础

1. 在 `tickets` 增加 `record_class`、`opened_by_id`、`assignment_group_id`、统一流程引用等缺失字段；
2. 建立 WorkItemRelation；
3. 抽取共享 WorkItem repository/service，但不接管专业状态机；
4. 评论、附件、活动、动态字段、SLA、审计接口统一接受 WorkItem ID；
5. 将现有 `service_requests.ticket_id` 明确作为逻辑 WorkItem ID 使用，本阶段不执行列重命名；
6. 增加数据完整性检查任务；
7. 发布统一事件和 Outbox。

### 18.4 Phase 2：Incident 迁移

1. 为每条 Incident 创建或匹配 WorkItem；
2. 新增 Incident `work_item_id` 唯一外键；
3. 回填公共字段并核对；
4. Incident 创建和状态变化改为事务性写 WorkItem；
5. 服务目录 Incident 创建改走同一专业服务；
6. 前端 Incident 详情使用统一 Shell；
7. 删除旧公共字段写入和读取；
8. 验证 Incident → Problem 关系。

Incident 优先迁移，因为它当前既有独立模型，又存在服务目录绕过 WorkItem 的明确断点。

### 18.5 Phase 3：Problem 与 Known Error

1. 为 Problem 建立 WorkItem；
2. 迁移公共字段；
3. Incident/Problem 关系迁入 WorkItemRelation；
4. 保留 Problem → Known Error 专业关系；
5. RCA、Workaround、Known Error 发布入口接入统一时间线和审计；
6. 删除重复调查入口，只保留权威服务。

### 18.6 Phase 4：Change Request

1. 为 Change 建立 WorkItem；
2. 迁移公共字段；
3. 将 `related_tickets` JSON 转换为结构化关系；
4. Incident/Problem/Requested Item 创建 Change 时同事务建立关系；
5. CAB、风险、窗口、实施、回滚和 PIR 继续由 ChangeService 管理；
6. 审批统一走 BPMN；
7. 删除旧 JSON 权威写路径。

### 18.7 Phase 5：服务请求层级

1. 将现有 ServiceRequest 在领域和产品语义上正式定位为 Requested Item，保留物理表名和
   `ticket_id` 列名，避免无收益 DDL；
2. 规范 Catalog Item 的 targetClass、分类、流程、模板和 SLA 绑定；
3. 增加按需 Catalog Task；
4. 当多项申请成为真实需求时再增加 Request Header；
5. 统一目录申请、履约、审批、交付和关闭证据。

### 18.8 Phase 6：清理与物理命名评估

1. 删除所有重复公共字段和遗留写路径；
2. 删除死服务、重复控制器和旧审批入口；
3. 清理 DTO 的 snake_case 兼容字段；
4. 决定是否将物理 `tickets` 表改名为 `work_items`；
5. 更新 OpenAPI、ER 图、运维手册和迁移 SOP。

## 19. 数据回填规则

### 19.1 公共字段冲突

当现有 Ticket 与专业实体字段不一致时：

1. 若已有明确关联 Ticket，以专业实体当前业务状态为迁移输入，但写入前执行状态映射校验；
2. 请求人、处理人和 tenant 必须验证实体存在与租户一致；
3. 最早 createdAt 作为 WorkItem createdAt；
4. 最新 updatedAt 作为 WorkItem updatedAt；
5. resolvedAt/closedAt 取与专业状态一致的有效值；
6. 冲突记录进入迁移异常报告，不静默选择。

### 19.2 编号

- `tickets.ticket_number` 是唯一权威业务编号列，直接保存带专业前缀的最终编号；
- 保留 Incident/Problem/Change 原编号并回填到对应 WorkItem，不因迁移重新编号；
- 通用 Ticket 保留现有 TKT/TICKET 编号，Requested Item、Request Header 和 Catalog Task 使用各自
  可配置前缀；
- 新编号按 tenant、recordClass、时间分区生成原子序列，不共享一个忽略专业类的全局计数器；
- 数据库唯一约束统一为 `(tenant_id, ticket_number)`；迁移前检查当前全局唯一索引和历史重复情况，
  在复合唯一索引生效后再移除旧索引；
- 生成器以数据库唯一约束作为最终并发防线，Redis 与数据库回退必须使用相同分区规则；
- 编号不可修改，也不因专业记录之间建立关系而复用编号；

### 19.3 孤儿记录

- 无合法 tenant、actor 或必需外键的记录不得自动伪造默认值；
- 输出隔离清单，由管理员修复或明确归档；
- 不硬编码 tenant 1、默认用户或默认分类。

## 20. 测试与验收

### 20.1 Schema 和迁移测试

- 每种专业记录均有唯一 WorkItem；
- recordClass 与扩展表匹配；
- 无跨租户外键或关系；
- 无孤儿扩展记录；
- 迁移前后数量、编号、状态和时间戳可对账；
- 迁移脚本重复执行不会产生重复记录。

### 20.2 服务测试

- 各专业状态机完整表驱动测试；
- 创建 WorkItem + 扩展失败时整体回滚；
- 关系类型、方向和租户校验；
- 权限和行级访问；
- 乐观锁；
- SLA 暂停/恢复；
- 审计和 Outbox 原子写入；
- AI/流程/连接器 actor 和来源记录。

### 20.3 合同测试

- 所有响应 camelCase；
- Controller 不返回 Ent；
- 专业详情包含统一公共字段和扩展字段；
- `actions` 与权限、状态一致；
- 旧 URL 在内部迁移后保持合同；
- 不再返回已删除的重复字段或兼容 snake_case。

### 20.4 端到端场景

1. 目录申请 → Requested Item → 审批 → Catalog Task → 交付 → 关闭；
2. 用户报障 → Incident → Workaround → 恢复 → 关闭；
3. 重复 Incident → Problem → RCA → Known Error；
4. Incident/Problem → Change → CAB → 实施 → PIR → 关闭；
5. Requested Item → Standard/Normal Change → 交付；
6. 邮件/监控/连接器创建记录并验证幂等；
7. MSP 操作者在授权客户 tenant 内操作并验证审计；
8. 跨租户读取和关系创建失败。

### 20.5 验证保真度

- 单元测试验证业务规则；
- 集成测试验证数据库事务、关系和租户；
- HTTP 合同测试验证 DTO 和授权；
- Playwright 验证真实浏览器路径和 http-client 转换；
- API 走查不能替代浏览器路径；
- 数据迁移必须直接核验数据库、事件 Outbox、缓存和外部索引状态。

## 21. 可观测性与运维

关键指标：

- 各 recordClass 创建量、积压量、解决时长；
- WorkItem/扩展完整性错误；
- 非法状态转换；
- 关系创建失败；
- SLA 违约；
- BPMN 触发和任务失败；
- Outbox 延迟和重试；
- 跨租户拒绝事件；
- 迁移异常和对账差异。

日志必须携带：

- tenantId；
- workItemId；
- recordClass；
- correlationId；
- actorId；
- workflowInstanceId（如有）。

日志禁止包含密码、JWT、连接器密钥和未保护的敏感申请内容。

## 22. 发布与回滚

### 22.1 发布

- 每个专业域使用独立迁移批次；
- 先在测试环境复制生产规模数据演练；
- 部署顺序：schema 扩展 → 回填 → 完整性验证 → 服务切换 → 旧字段删除；
- 服务切换和旧字段删除不能拆成长期双写状态；
- 大数据量迁移按 tenant 和 ID 区间分批，记录 checkpoint。

### 22.2 回滚

- 在旧字段删除前保留数据库快照和迁移映射表；
- 服务切换失败可回滚应用版本，但不得让新旧代码同时继续写不同权威字段；
- 已建立的新关系通过映射表可追踪；
- 删除字段和表属于不可逆阶段，必须在完整对账和观察窗口后执行。

## 23. 风险与缓解

| 风险 | 影响 | 缓解 |
|---|---|---|
| 把统一基类做成万能业务服务 | 专业规则退化 | 共享字段统一，状态机留在专业服务 |
| 长期双写 | 数据持续漂移 | 每域原子迁移，限定观察窗口后删除旧写路径 |
| API 兼容字段继续扩散 | 合同混乱 | DTO 合同测试和 camelCase 检查 |
| 关系表变成任意连接器 | 绕过领域约束 | 关系写入必须经过专业服务 |
| tenant 仅靠父记录保护 | 越权风险 | 关系和扩展查询同时带 tenant 条件 |
| BPMN/审批多栈未先收敛 | 新模型继续分叉 | 将审批收敛列为 WorkItem 横切能力前置依赖 |
| 大表迁移锁表 | 服务中断 | 分批回填、索引预建、演练和 checkpoint |
| 旧页面复制公共逻辑 | UX 和权限漂移 | 统一详情 Shell + 专业 Panel |
| `recordClass` 被当作可随意修改的 type | 历史损坏 | 扩展创建后不可变，转换采用新建和关系 |

## 24. 架构决策记录

### ADR-1：复用 `tickets` 作为 WorkItem 基表

接受。原因：

- 已经承载最多共享字段和横切能力；
- Service Request 已建立一对一委托先例；
- 减少新建基表后长期桥接旧 Ticket 的风险；
- 第一阶段无需物理重命名。

### ADR-2：专业域采用一对一扩展，而非单表继承

接受。原因：

- 保持专业字段、生命周期和服务边界；
- 避免巨型稀疏表和通用服务；
- 适配现有 Ent 结构和领域模块。

### ADR-3：Known Error 和 Catalog Item 不是 WorkItem

接受。它们分别是知识记录和服务定义，不是一次可分派工作执行。

### ADR-4：保留专业 API

接受。统一模型不等于统一成单一 `/tickets` API。专业行为继续通过专业 API 暴露。

### ADR-5：跨域流转采用“创建 + 关联”

接受。Incident 不转换为 Problem，Problem 不转换为 Change；创建目标记录并保存显式关系。

### ADR-6：Request Header 和 Catalog Task 按需引入

接受。当前单项申请不为了模拟 ServiceNow 而引入无实际业务价值的额外层级；当多项申请和拆分履约成为真实需求时启用。

## 25. 实施前置条件

进入实施计划前必须完成：

1. 用户批准本设计；
2. 确认现有生产数据规模、状态值和编号策略；
3. 确认 Service Request → Ticket 现有设计和实现已稳定；
4. 解决审批多栈的权威路径；
5. 选定 Incident 作为首个迁移域；
6. 为 Incident 主链路、跨租户和状态机建立回归基线；
7. 确认迁移期间允许的维护窗口和回滚策略。

## 26. 完成定义

只有满足以下条件，统一模型项目才算完成：

- 五类 WorkItem 均使用统一基础记录；
- 专业扩展不再持有重复公共字段；
- 所有创建和状态变化走权威专业服务；
- 服务目录 Incident 不再绕过 WorkItem；
- 跨域关系全部结构化且租户隔离；
- 评论、附件、SLA、流程、审批、审计、通知和时间线围绕 WorkItem 收敛；
- 前端使用统一 Shell 和专业 Panel；
- 旧写路径、旧 JSON 关系、重复控制器和兼容字段已删除；
- 全量后端测试、前端类型检查、相关 E2E 和迁移对账全部通过；
- 真实用户主链路完成端到端验证。

# 服务目录申请表单重新设计规范

- **文档版本**：v1.0（设计已定稿，待用户过目后转 writing-plans）
- **创建日期**：2026-08-21
- **状态**：已定稿待实施

---

## 1. 背景与问题

`itsm-frontend/src/app/(main)/service-catalog/request/[id]/page.tsx` 是所有服务目录申请共用的通用表单。用户在申请"Copilot采购申请"（`service_type=custom`）时发现，表单里出现了"成本中心""数据分级""需要公网IP""来源IP白名单""资源过期时间"等明显是为云资源/网络资源申请设计的字段，与实际业务不匹配。

进一步排查发现问题比"展示不合适"更严重：

1. **服务端无条件强制校验**：`itsm-backend/handlers/service_request/service.go:90-110` 对**所有** `service_type` 的申请都无条件要求 `ComplianceAck=true`、`ExpireAt` 非空且为未来时间、`DataClassification` 必须是四选一枚举值、以及勾选"需要公网IP"时 `SourceIPWhitelist` 非空——不管申请的是云服务器还是软件许可证。
2. **4 个字段是"假字段"**：申请人姓名、联系邮箱、数量、期望交付时间这四个输入框，提交后的值被塞进一个没有任何代码读取的 `form_data` JSON blob，从未真正落到可查询的字段上，纯属让用户做无用功。其中申请人/邮箱还与后端已有的、来自登录态的 `RequesterID` 完全重复。

## 2. 现状调查

### 2.1 数据库中真实的服务目录项分布（`service_catalogs` 表，24 条，含测试数据）

| service_type | 数量 | 举例 |
|---|---|---|
| custom | 8 | Copilot采购申请、特权账号申请、test系列 |
| access | 4 | 账号申请、权限变更 |
| vm | 2 | 云服务器申请 |
| network | 2 | 网络接入 |
| database | 2 | 数据库申请 |
| security | 2 | 安全扫描 |
| software | 2 | 软件安装 |
| devops | 2 | 代码仓库 |

`service_requests` 表目前只有 7 条真实提交记录（均为 dev 环境数据），历史数据无需回填迁移。

### 2.2 现有 12 个表单字段的真实归宿

| 字段 | 归宿 | 说明 |
|---|---|---|
| title（申请标题） | (a) 真实生效 | 写入 `Ticket.Title` |
| reason（申请理由） | (a) 真实生效 | 写入 `Ticket.Description` |
| costCenter（成本中心） | (a) 真实生效 | `ServiceRequest.CostCenter`，可空 |
| dataClassification（数据分级） | (a) 真实生效 | `ServiceRequest.DataClassification`，服务端强校验四选一 |
| needsPublicIp（需要公网IP） | (a) 真实生效 | `ServiceRequest.NeedsPublicIP`，服务端校验联动 IP 白名单 |
| sourceIpWhitelist（IP白名单） | (a) 真实生效 | `ServiceRequest.SourceIPWhitelist`，服务端强制校验 |
| expireAt（资源过期时间） | (a) 真实生效 | `ServiceRequest.ExpireAt`，服务端强制非空且未来时间 |
| complianceAck（合规确认） | (a) 真实生效 | `ServiceRequest.ComplianceAck`，服务端强制为 true |
| requesterName（申请人） | (b) 假字段 | 进 `form_data` JSON，无人读取，与已有 `RequesterID` 重复 |
| requesterEmail（联系邮箱） | (b) 假字段 | 同上 |
| quantity（数量） | (b) 假字段 | 进 `form_data` JSON，无人读取，无 ent 列 |
| expectedAt（期望交付时间） | (b) 假字段 | 同上，与目录项自带的 `catalog.deliveryTime` 是两个不同概念 |

补充信息区块（`该服务的补充信息`）走的是已有的 `FieldDefinition` 动态字段机制（每个目录项在后台可单独配置），这部分设计良好，本次不改。

## 3. 设计方案（已确认）

### 3.1 字段分层

**通用层（所有 service_type 都渲染 + 生效）：**

| 字段 | 现状 | 本次改动 |
|---|---|---|
| 申请标题 title | 真实生效 | 不变 |
| 申请理由 reason | 真实生效 | 不变 |
| 联系人姓名 contactName | 假字段 | **改为真实字段**，默认取当前登录用户，可编辑覆盖（支持代他人提交场景），真正落库并在审批/处理页展示 |
| 联系人邮箱 contactEmail | 假字段 | 同上 |
| 数量 quantity | 假字段 | **改为真实字段**，可选，默认 1，落库并展示 |
| 期望交付时间 expectedAt | 假字段 | **改为真实字段**，可选日期，落库并展示 |
| 补充信息（FieldDefinition） | 已实现 | 不变 |

**基础设施层（仅 `requiresInfraFields=true` 的目录项渲染 + 强制校验）：**

成本中心 / 数据分级 / 需要公网IP / 来源IP白名单 / 资源过期时间 / 合规确认——这 6 个字段维持现有 `ServiceRequest` 表结构不变（本来就可空或有默认值），只改变"何时展示、何时强制校验"。

`requiresInfraFields` 的判定范围：仅 `service_type ∈ {vm, network, database}`。

### 3.2 数据模型改动

`service_requests` 表新增 4 列（一次性 additive 迁移，无需回填）：
- `contact_name varchar` （可空）
- `contact_email varchar` （可空）
- `quantity int` （默认 1）
- `expected_at timestamptz` （可空）

现有 6 个基础设施字段列**不改表结构**。

### 3.3 gating 逻辑单一来源（AGENTS.md 合规性修正）

最初讨论时设想前端自行判断 `catalog.serviceType` 是否属于 `vm/network/database`。经对照 AGENTS.md 的 "No Hardcoding" 规则与"itsm-frontend 不应重复属于后端的业务规则"边界原则，**改为**：

- 后端在 `ServiceCatalog` 的 DTO/Mapper 中新增一个计算字段 `requiresInfraFields: boolean`，由**唯一一处** Go 函数（类似现有 `isTicketDataScopeAllRole` 的分类辅助函数模式）根据 `service_type` 计算得出。
- 前端**不**判断 `service_type`，只读取后端算好的 `catalog.requiresInfraFields` 决定是否渲染基础设施区块。
- 后端 `Create` 校验调用同一个判定函数，决定是否执行现有那 4 段强制校验。

这样"哪些 service_type 需要基础设施字段"这条业务规则只存在于后端一处，避免前后端各写一份导致漂移。

### 3.4 前端渲染改动

`request/[id]/page.tsx` 现有一大段写死的 `<Form.Item>` 拆分为：
- `<CommonRequestFields />`：通用层，永远渲染（含默认预填当前登录用户的联系人姓名/邮箱）
- `<InfrastructureRequestFields />`：仅当 `catalog.requiresInfraFields === true` 时渲染

现有"补充信息"动态字段区块位置不变，接在这两块下面。

### 3.5 后端校验改动

`handlers/service_request/service.go` 的 `Create`：现在无条件执行的 `ComplianceAck` / `NeedsPublicIP+Whitelist` / `ExpireAt` / `DataClassification` 四段校验，改为先查 `requiresInfraFields(cat.ServiceType)`，仅为 true 时才执行；否则跳过，对应字段落库时使用 ent 列默认值。

DTO（`CreateServiceRequestRequest`）新增 `contactName`/`contactEmail`/`quantity`/`expectedAt` 四个可选字段，直接映射到新增列，不再经过 `form_data` JSON 兜底路径。

> **实现注意**：`quantity` 默认值 1 不能只依赖 ent schema 的 `.Default(1)`——如果 service 层显式调用 `SetQuantity(0)`（比如前端传了 0 或未传导致 Go 零值），ent 会写入 0 而不是触发 schema 默认值。必须在 service 层显式兜底：`quantity <= 0` 时才置为 1，再传给 ent builder。

`ServiceRequestPanel.tsx`（审批/处理详情页）补上这四个新字段的展示。

## 4. AGENTS.md 合规性检查记录

1. **No Hardcoding**：已修正为 3.3 所述的单一后端判定源，不在前后端各写一份 gating 列表。
2. **历史教训直接命中**（AGENTS.md「复杂功能开发经验教训」）：字段名被前端 http-client 全局 camelCase 转换悄悄改写、后端精确匹配失败、值被静默丢弃——这个缺陷类别**已经在"服务申请提交"这条路径上出现过一次**，正是 `requesterName`/`quantity` 等字段沦为假字段的历史原因之一。本次实现完成后，验证必须：
   - 通过真实前端 http-client 提交表单（不能只用 curl 直连后端），确认 camelCase 请求体字段名与 Go DTO 的 json tag 精确匹配；
   - 验证报告中明确写清楚"这次测试覆盖了浏览器提交这一层，不是只测了后端"。
3. **DTO/字段命名规范**：新增字段遵循 ent snake_case（`contact_name` 等）→ DTO/前端 camelCase（`contactName` 等）的既有约定。
4. **租户隔离**：新增列挂在已有 `service_requests` 表上，该表已有 `tenant_id` 且查询已走租户过滤，无需额外处理。
5. **分层规范**：改动全部落在 `handlers/service_request/`（domain-sliced 新分层），不触碰 `controller/`+`service/` 旧分层。

## 5. 迁移与测试计划

### 5.1 数据库迁移

一次 ent migration，`service_requests` 表新增 4 列，全部 additive、可空/有默认值，无需回填历史数据（当前仅 7 条真实记录，均为 dev 环境）：

- `contact_name varchar`（可空）
- `contact_email varchar`（可空）
- `quantity int`（默认 1）
- `expected_at timestamptz`（可空）

### 5.2 后端测试（`handlers/service_request/service_test.go`，扩展现有表格驱动测试）

- `requiresInfraFields` 判定函数单元测试：`vm`/`network`/`database` → `true`；`custom`/`access`/`security`/`software`/`devops` → `false`
- `Create`：非基础设施类型目录项提交时，不传 6 个基础设施字段也能创建成功（当前行为会被拒绝，属于本次要修的回归）
- `Create`：基础设施类型目录项仍然维持原有 4 段强制校验不放松（`ComplianceAck`/`NeedsPublicIP+Whitelist`/`ExpireAt`/`DataClassification`）
- `Create`：`contactName`/`contactEmail`/`quantity`/`expectedAt` 传入后能在返回的 `ServiceRequest` 中读到，不再落入 `form_data` JSON 兜底路径

### 5.3 前端测试

- `npm run type-check`
- Jest 测试覆盖 `<InfrastructureRequestFields />`：`catalog.requiresInfraFields=false` 时不渲染该区块
- **手工验证必须走真实浏览器提交路径**（呼应第 4 节第 2 条历史教训，不能只用 curl 直连后端）：登录后分别对一个 `custom` 类目录项（如 Copilot采购申请）和一个 `vm` 类目录项（如云服务器申请）提交，确认：
  - 基础设施字段按 `requiresInfraFields` 预期显示/隐藏
  - 提交后能在处理/审批详情页看到 `contactName`/`contactEmail`/`quantity`/`expectedAt` 的值（验证 camelCase 请求体字段与后端 DTO json tag 精确匹配，值未被静默丢弃）

# 服务目录申请通用表单重设计 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让服务目录申请表单（`/service-catalog/request/[id]`）按服务类型分层：通用字段对所有服务目录项生效且真实落库，"成本中心/数据分级/公网IP/IP白名单/资源过期时间/合规确认"这组基础设施字段只对 `vm`/`network`/`database` 三类目录项展示与强制校验。

**Architecture:** 后端 `ServiceCatalog` 新增一个由 service_type 计算出的 `requiresInfraFields` 布尔值，作为"是否需要基础设施字段"这条业务规则的唯一判定源，前端只读取它、不重复判断逻辑；后端 `ServiceRequest` 新增 4 个真实持久化列（`contact_name`/`contact_email`/`quantity`/`expected_at`）替换掉现在只进 `form_data` JSON 就再没人读的假字段。

**Tech Stack:** Go 1.25 + Gin + Ent ORM + PostgreSQL（后端，`itsm-backend/`）；Next.js App Router + TypeScript + Ant Design + Zustand（前端，`itsm-frontend/`）。

**Spec:** `docs/superpowers/specs/2026-08-21-service-catalog-request-form-redesign-design.md`

## Global Constraints

- 所有新增 HTTP 请求/响应字段使用 camelCase（如 `requiresInfraFields`、`contactName`），Ent schema/数据库列使用 snake_case（如 `contact_name`）——见 CLAUDE.md/AGENTS.md 的字段命名规范。
- `service_type ∈ {vm, network, database}` 才需要基础设施字段组；这条判断只在后端 `handlers/service_catalog` 包里实现一次（`RequiresInfraFields` 函数），前端不得自行重复判断 `service_type`。
- 新增列必须可空或有默认值，不需要回填历史数据（当前 `service_requests` 表仅 7 条真实记录，均为 dev 环境）。
- 改动全部落在 `handlers/<domain>/`（domain-sliced 新分层），不得触碰 `controller/`+`service/` 旧分层。
- 后端改动完成后跑：`cd itsm-backend && go build ./... && go test ./handlers/service_catalog/... ./handlers/service_request/...`；前端改动完成后跑：`cd itsm-frontend && npm run type-check`。
- 涉及字段提交路径的手工验证，必须走真实前端 http-client 提交（浏览器实际提交表单），不能只用 curl 直连后端——这条 codebase 里已经踩过一次"camelCase 字段被静默丢弃"的坑（AGENTS.md「复杂功能开发经验教训」）。

---

### Task 1: ServiceRequest 新增 4 个持久化字段 + 数据库迁移

**Files:**
- Modify: `itsm-backend/ent/schema/servicerequest.go`
- Modify: `itsm-backend/migration/migrations.go`

**Interfaces:**
- Consumes: 无（本任务是最底层的 schema 改动）
- Produces: Ent 生成的 `ent.ServiceRequest` 结构体新增字段 `ContactName string`、`ContactEmail string`、`Quantity int`、`ExpectedAt time.Time`（非指针，零值表示未设置，与现有 `ExpireAt` 字段的约定一致），供 Task 3 使用。

- [ ] **Step 1: 在 Ent schema 里加 4 个字段**

打开 `itsm-backend/ent/schema/servicerequest.go`，在 `form_data` 字段组（`compliance_ack` 那一行）之后加入：

```go
		// 表单数据
		field.JSON("form_data", map[string]any{}).Comment("表单数据").Optional(),
		field.String("cost_center").Comment("成本中心").Optional(),
		field.String("data_classification").Comment("数据分级：public|internal|confidential").Default("internal"),
		field.Bool("needs_public_ip").Comment("是否需要公网访问").Default(false),
		field.JSON("source_ip_whitelist", []string{}).Comment("源IP白名单").Optional(),
		field.Time("expire_at").Comment("到期时间").Optional(),
		field.Bool("compliance_ack").Comment("合规条款确认").Default(false),

		// 通用层字段：所有 service_type 都适用，取代原来只进 form_data 就没人读的假字段
		field.String("contact_name").Comment("联系人姓名，默认取申请人姓名，可编辑以支持代他人提交").Optional(),
		field.String("contact_email").Comment("联系人邮箱，默认取申请人邮箱，可编辑以支持代他人提交").Optional(),
		field.Int("quantity").Comment("申请数量").Default(1).Positive(),
		field.Time("expected_at").Comment("期望交付时间").Optional(),
```

- [ ] **Step 2: 重新生成 Ent 代码**

```bash
cd itsm-backend && go generate ./ent
```

- [ ] **Step 3: 确认生成的结构体包含新字段**

```bash
grep -n "ContactName\|ContactEmail\|Quantity\|ExpectedAt" ent/servicerequest.go
```

Expected: 看到 `ContactName string`、`ContactEmail string`、`Quantity int`、`ExpectedAt time.Time` 四行。

- [ ] **Step 4: 确认整个后端仍然编译通过**

```bash
go build ./...
```

Expected: 无输出，退出码 0。

- [ ] **Step 5: 注册新迁移**

打开 `itsm-backend/migration/migrations.go`，在 `RegisteredMigrations` 切片的 `014_drop_legacy_approval_workflow` 条目后追加：

```go
	{
		Version:     "015_add_service_request_contact_fields",
		Description: "Add contact_name/contact_email/quantity/expected_at columns to service_requests (previously fake fields that only lived in form_data and were never read back)",
		RollbackSQL: "ALTER TABLE service_requests DROP COLUMN IF EXISTS contact_name; ALTER TABLE service_requests DROP COLUMN IF EXISTS contact_email; ALTER TABLE service_requests DROP COLUMN IF EXISTS quantity; ALTER TABLE service_requests DROP COLUMN IF EXISTS expected_at;",
	},
```

然后在 `GetMigrationSQL` 函数的 `switch` 里，在 `case "014_drop_legacy_approval_workflow":` 分支之后加入新 case：

```go
	case "015_add_service_request_contact_fields":
		return `
ALTER TABLE service_requests ADD COLUMN IF NOT EXISTS contact_name VARCHAR;
ALTER TABLE service_requests ADD COLUMN IF NOT EXISTS contact_email VARCHAR;
ALTER TABLE service_requests ADD COLUMN IF NOT EXISTS quantity BIGINT NOT NULL DEFAULT 1;
ALTER TABLE service_requests ADD COLUMN IF NOT EXISTS expected_at TIMESTAMP WITH TIME ZONE;
`
```

- [ ] **Step 6: 跑迁移前先看当前状态（防御性检查）**

这个 dev 库之前发现过"迁移记录显示已应用，但实际 DDL 没生效"的漂移（`013_service_request_delegates_to_ticket` 就是一个例子——迁移状态显示 applied，但 `status`/`title`/`reason` 等列至今还在表里）。跑新迁移前后都用 `\d` 直接看一眼真实列，不要只信 `-status` 的输出：

```bash
PGPASSWORD=dev123 psql -h localhost -p 5432 -U itsm_user -d itsm -c "\d service_requests" | grep -E "contact_name|contact_email|quantity|expected_at"
```

Expected: 这一步应该没有任何输出（列还不存在）。

- [ ] **Step 7: 执行迁移**

```bash
go run -tags migrate ./cmd/migrate -up
```

- [ ] **Step 8: 用真实 SQL 验证列已经存在（不要只看迁移工具的成功提示）**

```bash
PGPASSWORD=dev123 psql -h localhost -p 5432 -U itsm_user -d itsm -c "\d service_requests" | grep -E "contact_name|contact_email|quantity|expected_at"
```

Expected: 看到 4 行，分别是 `contact_name`、`contact_email`、`quantity`（`bigint`, default `1`）、`expected_at`。如果这一步看不到列，说明迁移工具的"applied"提示不可信，必须停下来排查，不能继续下一个 Task。

- [ ] **Step 9: Commit**

```bash
git add itsm-backend/ent/schema/servicerequest.go itsm-backend/ent/servicerequest.go itsm-backend/ent/servicerequest itsm-backend/ent/migrate itsm-backend/migration/migrations.go
git commit -m "feat(service-request): add contact_name/contact_email/quantity/expected_at columns"
```

---

### Task 2: ServiceCatalog 新增 `RequiresInfraFields` 判定 + DTO 暴露

**Files:**
- Modify: `itsm-backend/handlers/service_catalog/entity.go`
- Modify: `itsm-backend/handlers/service_catalog/repository_impl.go`
- Modify: `itsm-backend/handlers/service_catalog/handler.go`
- Modify: `itsm-backend/dto/service_dto.go`
- Create: `itsm-backend/handlers/service_catalog/entity_test.go`
- Test: `itsm-backend/handlers/service_catalog/repository_impl_test.go`（追加一个测试）

**Interfaces:**
- Consumes: 无
- Produces: `service_catalog.RequiresInfraFields(serviceType string) bool`（导出函数，Task 4 会在 `handlers/service_request/service.go` 里调用它）；`dto.ServiceCatalogResponse.RequiresInfraFields bool`（JSON 字段 `requiresInfraFields`，Task 6 前端会读它）。

- [ ] **Step 1: 写失败的单元测试（纯函数，不需要数据库）**

创建 `itsm-backend/handlers/service_catalog/entity_test.go`：

```go
package service_catalog

import "testing"

func TestRequiresInfraFields(t *testing.T) {
	cases := []struct {
		serviceType string
		want        bool
	}{
		{"vm", true},
		{"network", true},
		{"database", true},
		{"custom", false},
		{"access", false},
		{"security", false},
		{"software", false},
		{"devops", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.serviceType, func(t *testing.T) {
			got := RequiresInfraFields(tc.serviceType)
			if got != tc.want {
				t.Errorf("RequiresInfraFields(%q) = %v, want %v", tc.serviceType, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd itsm-backend && go test ./handlers/service_catalog/... -run TestRequiresInfraFields -v
```

Expected: 编译失败，提示 `undefined: RequiresInfraFields`。

- [ ] **Step 3: 实现 `RequiresInfraFields`，并把 `ServiceType` 加进领域结构体**

打开 `itsm-backend/handlers/service_catalog/entity.go`，在 `ServiceCatalog` 结构体的 `Status` 字段后加入 `ServiceType`：

```go
type ServiceCatalog struct {
	ID             int
	Name           string
	Category       string
	Description    string
	ITSMType       string // Request|Incident|Change，决定审批路由
	ServiceType    string // vm|network|database|access|security|software|devops|custom，决定是否需要基础设施字段（见 RequiresInfraFields）
	DeliveryTime   int
	CITypeID       int
	CloudServiceID int
	// ProcessDefinitionKey 是该目录条目专属的 BPMN 流程定义 Key（可选）。非空时优先于
	// businessType+businessSubType 的通用流程绑定解析，见 ticket_service.go
	// triggerWorkflowForTicket 的 workflowDefinitionKey 参数。
	ProcessDefinitionKey string
	Status               string
	TenantID             int
	Fields               []service.FieldDefinitionInput
	CreatedAt            time.Time
	UpdatedAt            time.Time
}
```

在文件末尾（`ListFilters`/`Repository` 定义之后）加入判定函数：

```go
// RequiresInfraFields 判断该服务类型是否需要基础设施类字段（成本中心/数据分级/
// 需要公网IP/来源IP白名单/资源过期时间/合规确认）。这条业务规则只在这一处实现——
// 前端只读取 ServiceCatalogResponse.RequiresInfraFields，不自行判断 service_type，
// 后端 Create 校验也调用这同一个函数，避免两处各写一份导致漂移。
func RequiresInfraFields(serviceType string) bool {
	switch serviceType {
	case "vm", "network", "database":
		return true
	default:
		return false
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./handlers/service_catalog/... -run TestRequiresInfraFields -v
```

Expected: `PASS`，9 个子测试全部通过。

- [ ] **Step 5: 把 `ServiceType` 接进 Ent↔领域模型的转换**

打开 `itsm-backend/handlers/service_catalog/repository_impl.go`，在 `toDomain` 函数里的 `Category` 那一行后加入：

```go
func (r *EntRepository) toDomain(e *ent.ServiceCatalog) *ServiceCatalog {
	return &ServiceCatalog{
		ID:                   e.ID,
		Name:                 e.Name,
		Category:             e.Category,
		Description:          e.Description,
		ITSMType:             e.ItsmType,
		ServiceType:          e.ServiceType,
		DeliveryTime:         e.DeliveryTime,
		CITypeID:             e.CiTypeID,
		CloudServiceID:       e.CloudServiceID,
		ProcessDefinitionKey: e.ProcessDefinitionKey,
		Status:               e.Status,
		TenantID:             e.TenantID,
		CreatedAt:            e.CreatedAt,
		UpdatedAt:            e.UpdatedAt,
	}
}
```

- [ ] **Step 6: 写一个失败的仓储层测试，证明 `ServiceType` 真的透传过来了**

打开 `itsm-backend/handlers/service_catalog/repository_impl_test.go`，在文件末尾追加：

```go
func TestEntRepository_ToDomain_CarriesServiceType(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_type?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx := context.Background()
	repo := NewEntRepository(client)

	created, err := client.ServiceCatalog.Create().
		SetName("云服务器申请").
		SetCategory("云资源").
		SetDescription("desc").
		SetDeliveryTime(1).
		SetStatus("active").
		SetTenantID(1).
		SetServiceType("vm").
		Save(ctx)
	require.NoError(t, err)

	got, err := repo.Get(ctx, 1, created.ID)
	require.NoError(t, err)
	require.Equal(t, "vm", got.ServiceType)
}
```

- [ ] **Step 7: 跑测试确认通过**

```bash
go test ./handlers/service_catalog/... -run TestEntRepository_ToDomain_CarriesServiceType -v
```

Expected: `PASS`。

- [ ] **Step 8: 在 DTO 里加 `RequiresInfraFields`**

打开 `itsm-backend/dto/service_dto.go`，在 `ServiceCatalogResponse` 结构体的 `Status` 字段后加入：

```go
// ServiceCatalogResponse 服务目录响应
type ServiceCatalogResponse struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Category       string `json:"category"`
	Description    string `json:"description"`
	DeliveryTime   string `json:"deliveryTime"`
	CITypeID       int    `json:"ciTypeId,omitempty"`
	CloudServiceID int    `json:"cloudServiceId,omitempty"`
	// ProcessDefinitionKey 是该目录条目专属的 BPMN 流程定义 Key（可选），非空时优先于
	// businessType+businessSubType 的通用流程绑定解析。
	ProcessDefinitionKey string `json:"processDefinitionKey,omitempty"`
	Status               string `json:"status"`
	// RequiresInfraFields 由后端根据 service_type 计算：仅 vm/network/database 为 true。
	// 前端申请表单据此决定是否渲染"成本中心/数据分级/公网IP/IP白名单/资源过期时间/
	// 合规确认"这组基础设施字段，不自行判断 service_type（见 RequiresInfraFields 注释）。
	RequiresInfraFields bool                      `json:"requiresInfraFields"`
	Fields              []map[string]interface{} `json:"fields,omitempty"`
	CreatedAt           time.Time                 `json:"createdAt"`
	UpdatedAt           time.Time                 `json:"updatedAt"`
}
```

- [ ] **Step 9: 在 mapper 里计算它**

打开 `itsm-backend/handlers/service_catalog/handler.go`，在 `toDTO` 函数里加入 `RequiresInfraFields`：

```go
func (h *Handler) toDTO(c *ServiceCatalog) dto.ServiceCatalogResponse {
	fields := make([]map[string]interface{}, 0, len(c.Fields))
	for _, d := range c.Fields {
		fields = append(fields, map[string]interface{}{
			"name": d.Name, "label": d.Label, "type": d.FieldType,
			"required": d.Required, "options": d.Options,
		})
	}
	return dto.ServiceCatalogResponse{
		ID:                   c.ID,
		Name:                 c.Name,
		Category:             c.Category,
		Description:          c.Description,
		DeliveryTime:         strconv.Itoa(c.DeliveryTime),
		CITypeID:             c.CITypeID,
		CloudServiceID:       c.CloudServiceID,
		ProcessDefinitionKey: c.ProcessDefinitionKey,
		Status:               c.Status,
		RequiresInfraFields:  RequiresInfraFields(c.ServiceType),
		Fields:               fields,
		CreatedAt:            c.CreatedAt,
		UpdatedAt:            c.UpdatedAt,
	}
}
```

- [ ] **Step 10: 确认整个包编译通过且已有测试全绿**

```bash
go build ./... && go test ./handlers/service_catalog/... -v 2>&1 | tail -40
```

Expected: 所有测试 `PASS`，无编译错误。

- [ ] **Step 11: Commit**

```bash
git add itsm-backend/handlers/service_catalog/entity.go itsm-backend/handlers/service_catalog/entity_test.go itsm-backend/handlers/service_catalog/repository_impl.go itsm-backend/handlers/service_catalog/repository_impl_test.go itsm-backend/handlers/service_catalog/handler.go itsm-backend/dto/service_dto.go
git commit -m "feat(service-catalog): add RequiresInfraFields classification, expose via DTO"
```

---

### Task 3: ServiceRequest 新字段端到端打通（领域层/仓储层/Handler/DTO）

**Files:**
- Modify: `itsm-backend/handlers/service_request/entity.go`
- Modify: `itsm-backend/handlers/service_request/repository_impl.go`
- Modify: `itsm-backend/handlers/service_request/handler.go`
- Modify: `itsm-backend/dto/service_dto.go`
- Test: `itsm-backend/handlers/service_request/repository_impl_test.go`

**Interfaces:**
- Consumes: Task 1 产出的 `ent.ServiceRequest.ContactName/ContactEmail/Quantity/ExpectedAt`
- Produces: `service_request.ServiceRequest{ContactName, ContactEmail, Quantity, ExpectedAt}` 领域字段；`dto.CreateServiceRequestRequest{ContactName, ContactEmail, Quantity, ExpectedAt}`；`dto.ServiceRequestResponse{ContactName, ContactEmail, Quantity, ExpectedAt}`，供 Task 5（前端）消费。

- [ ] **Step 1: 写一个失败的仓储层往返测试**

打开 `itsm-backend/handlers/service_request/repository_impl_test.go`（如果文件不存在则新建，import 参照 `service_test.go` 顶部），在文件末尾追加：

```go
func TestEntRepository_Create_PersistsContactAndQuantityFields(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_contact_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-contact").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	ticket, err := client.Ticket.Create().
		SetTitle("测试工单").SetDescription("desc").SetPriority("medium").SetStatus("open").
		SetType("service_request").SetTenantID(tenant.ID).SetRequesterID(1).SetTicketNumber("T-1").
		Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	expected := time.Now().Add(48 * time.Hour)
	created, err := repo.Create(ctx, &ServiceRequest{
		TenantID:           tenant.ID,
		TicketID:           ticket.ID,
		CatalogID:          1,
		RequesterID:        1,
		DataClassification: "internal",
		ContactName:        "李四",
		ContactEmail:       "lisi@example.com",
		Quantity:           3,
		ExpectedAt:         &expected,
	})
	require.NoError(t, err)

	require.Equal(t, "李四", created.ContactName)
	require.Equal(t, "lisi@example.com", created.ContactEmail)
	require.Equal(t, 3, created.Quantity)
	require.NotNil(t, created.ExpectedAt)
	require.WithinDuration(t, expected, *created.ExpectedAt, time.Second)

	fetched, err := repo.Get(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, "李四", fetched.ContactName)
	require.Equal(t, 3, fetched.Quantity)
}

func TestEntRepository_Create_QuantityDefaultsToOneWhenOmitted(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_quantity_default?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-qty-default").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	ticket, err := client.Ticket.Create().
		SetTitle("测试工单").SetDescription("desc").SetPriority("medium").SetStatus("open").
		SetType("service_request").SetTenantID(tenant.ID).SetRequesterID(1).SetTicketNumber("T-2").
		Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	created, err := repo.Create(ctx, &ServiceRequest{
		TenantID:           tenant.ID,
		TicketID:           ticket.ID,
		CatalogID:          1,
		RequesterID:        1,
		DataClassification: "internal",
		// Quantity 不设置，Go 零值是 0
	})
	require.NoError(t, err)
	require.Equal(t, 1, created.Quantity, "Quantity 未提供时应落到 ent schema 的默认值 1，而不是 0")
}
```

如果 `repository_impl_test.go` 是新建文件，文件头部需要：

```go
package service_request

import (
	"context"
	"testing"
	"time"

	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd itsm-backend && go test ./handlers/service_request/... -run "TestEntRepository_Create_PersistsContactAndQuantityFields|TestEntRepository_Create_QuantityDefaultsToOneWhenOmitted" -v
```

Expected: 编译失败，`ServiceRequest` 结构体没有 `ContactName`/`ContactEmail`/`Quantity`/`ExpectedAt` 字段。

- [ ] **Step 3: 给领域结构体加 4 个字段**

打开 `itsm-backend/handlers/service_request/entity.go`，在 `ServiceRequest` 结构体的 `ComplianceAckSet` 字段后加入：

```go
type ServiceRequest struct {
	ID                 int
	TenantID           int
	TicketID           int
	CatalogID          int
	RequesterID        int
	CiID               int
	FormData           map[string]interface{}
	CostCenter         string
	DataClassification string
	NeedsPublicIP      bool
	NeedsPublicIPSet   bool
	SourceIPWhitelist  []string
	ExpireAt           *time.Time
	ComplianceAck      bool
	ComplianceAckSet   bool
	// 通用层字段：所有 service_type 都适用，取代原来只进 FormData 就没人读的假字段。
	ContactName  string
	ContactEmail string
	Quantity     int
	ExpectedAt   *time.Time
	Version      int
	ProcessorID  *int
	StartedAt    *time.Time
	CompletedAt  *time.Time
	CompletionNote string
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time

	TicketTitle  string
	TicketStatus string
}
```

- [ ] **Step 4: 在仓储层的 `toDomain`/`Create`/`Update` 里接上这 4 个字段**

打开 `itsm-backend/handlers/service_request/repository_impl.go`。

在 `toDomain` 里，`ComplianceAck` 那一行后加入：

```go
func (r *EntRepository) toDomain(req *ent.ServiceRequest) *ServiceRequest {
	if req == nil {
		return nil
	}
	return &ServiceRequest{
		ID:                 req.ID,
		TenantID:           req.TenantID,
		TicketID:           req.TicketID,
		CatalogID:          req.CatalogID,
		RequesterID:        req.RequesterID,
		CiID:               req.CiID,
		FormData:           req.FormData,
		CostCenter:         req.CostCenter,
		DataClassification: req.DataClassification,
		NeedsPublicIP:      req.NeedsPublicIP,
		SourceIPWhitelist:  req.SourceIPWhitelist,
		ExpireAt:           itemOrNil(req.ExpireAt),
		ComplianceAck:      req.ComplianceAck,
		ContactName:        req.ContactName,
		ContactEmail:       req.ContactEmail,
		Quantity:           req.Quantity,
		ExpectedAt:         itemOrNil(req.ExpectedAt),
		Version:            req.Version,
		ProcessorID:        optionalInt(req.ProcessorID),
		StartedAt:          itemOrNil(req.StartedAt),
		CompletedAt:        itemOrNil(req.CompletedAt),
		CompletionNote:     req.CompletionNote,
		LastError:          req.LastError,
		CreatedAt:          req.CreatedAt,
		UpdatedAt:          req.UpdatedAt,
	}
}
```

在 `Create` 里，`SetCiID` 那段之前加入（与 `CostCenter` 完全一致的"非空才设置"模式，`Quantity` 用 `> 0` 保证 0/未提供时落到 ent schema 的 `.Default(1)`）：

```go
	if req.FormData != nil {
		create.SetFormData(req.FormData)
	}
	if req.CostCenter != "" {
		create.SetCostCenter(req.CostCenter)
	}
	if req.SourceIPWhitelist != nil {
		create.SetSourceIPWhitelist(req.SourceIPWhitelist)
	}
	if req.ExpireAt != nil {
		create.SetExpireAt(*req.ExpireAt)
	}
	if req.ContactName != "" {
		create.SetContactName(req.ContactName)
	}
	if req.ContactEmail != "" {
		create.SetContactEmail(req.ContactEmail)
	}
	if req.Quantity > 0 {
		create.SetQuantity(req.Quantity)
	}
	if req.ExpectedAt != nil {
		create.SetExpectedAt(*req.ExpectedAt)
	}
	if req.CiID > 0 {
		create.SetCiID(req.CiID)
	}
```

在 `Update` 函数里（`SetSourceIPWhitelist` 那一段附近）同样加入：

```go
		SetFormData(req.FormData).
		SetCostCenter(req.CostCenter).
		SetDataClassification(req.DataClassification).
		SetNeedsPublicIP(req.NeedsPublicIP).
		SetSourceIPWhitelist(req.SourceIPWhitelist).
```

改为：

```go
		SetFormData(req.FormData).
		SetCostCenter(req.CostCenter).
		SetDataClassification(req.DataClassification).
		SetNeedsPublicIP(req.NeedsPublicIP).
		SetSourceIPWhitelist(req.SourceIPWhitelist).
		SetContactName(req.ContactName).
		SetContactEmail(req.ContactEmail).
```

（`Quantity`/`ExpectedAt` 不加入 `Update` 路径——现有 `UpdateServiceRequestRequest` DTO 本来就没有开放这两个字段的更新入口，本次也不新增，保持改动范围最小。）

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./handlers/service_request/... -run "TestEntRepository_Create_PersistsContactAndQuantityFields|TestEntRepository_Create_QuantityDefaultsToOneWhenOmitted" -v
```

Expected: 两个测试都 `PASS`。

- [ ] **Step 6: DTO 加字段**

打开 `itsm-backend/dto/service_dto.go`，`CreateServiceRequestRequest` 加入：

```go
type CreateServiceRequestRequest struct {
	CatalogID int            `json:"catalogId" binding:"omitempty,min=1"`
	Title     string         `json:"title" binding:"omitempty,max=255"`
	Reason    string         `json:"reason" binding:"omitempty,max=500"`
	FormData  map[string]any `json:"formData" binding:"omitempty"`

	CostCenter         string     `json:"costCenter" binding:"omitempty,max=100"`
	DataClassification string     `json:"dataClassification" binding:"omitempty,oneof=public internal confidential restricted"`
	NeedsPublicIP      bool       `json:"needsPublicIp"`
	SourceIPWhitelist  []string   `json:"sourceIpWhitelist" binding:"omitempty"`
	ExpireAt           *time.Time `json:"expireAt" binding:"omitempty"`
	ComplianceAck      bool       `json:"complianceAck"`

	// 通用层字段：所有 service_type 都适用。
	ContactName  string     `json:"contactName" binding:"omitempty,max=100"`
	ContactEmail string     `json:"contactEmail" binding:"omitempty,max=255,email"`
	Quantity     int        `json:"quantity" binding:"omitempty,min=1,max=1000"`
	ExpectedAt   *time.Time `json:"expectedAt" binding:"omitempty"`
}
```

`ServiceRequestResponse` 加入（放在 `ComplianceAck` 字段后）：

```go
type ServiceRequestResponse struct {
	ID          int            `json:"id"`
	TicketID    int            `json:"ticketId"`
	CatalogID   int            `json:"catalogId"`
	RequesterID int            `json:"requesterId"`
	CIID        int            `json:"ciId,omitempty"`
	FormData    map[string]any `json:"formData,omitempty"`

	CostCenter         string     `json:"costCenter,omitempty"`
	DataClassification string     `json:"dataClassification,omitempty"`
	NeedsPublicIP      bool       `json:"needsPublicIp"`
	SourceIPWhitelist  []string   `json:"sourceIpWhitelist,omitempty"`
	ExpireAt           *time.Time `json:"expireAt,omitempty"`
	ComplianceAck      bool       `json:"complianceAck"`

	ContactName  string     `json:"contactName,omitempty"`
	ContactEmail string     `json:"contactEmail,omitempty"`
	Quantity     int        `json:"quantity"`
	ExpectedAt   *time.Time `json:"expectedAt,omitempty"`

	Version        int        `json:"version"`
	ProcessorID    *int       `json:"processorId,omitempty"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
	CompletionNote string     `json:"completionNote,omitempty"`
	LastError      string     `json:"lastError,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`

	Catalog      *ServiceCatalogResponse    `json:"catalog,omitempty"`
	Requester    *UserResponse              `json:"requester,omitempty"`
	CustomFields []CustomFieldValueResponse `json:"customFields,omitempty"`

	TicketTitle  string `json:"ticketTitle,omitempty"`
	TicketStatus string `json:"ticketStatus,omitempty"`
}
```

（`Version` 及之后的字段是文件里原有内容，照抄不动，只是把新字段插进 `ComplianceAck` 和 `Version` 之间。）

- [ ] **Step 7: Handler 里把 DTO 字段接到领域对象、领域对象接回响应**

打开 `itsm-backend/handlers/service_request/handler.go`。

`Create` 函数里 `domainReq` 构造加入：

```go
	domainReq := &ServiceRequest{
		ComplianceAck:      req.ComplianceAck,
		NeedsPublicIP:      req.NeedsPublicIP,
		DataClassification: req.DataClassification,
		FormData:           req.FormData,
		CostCenter:         req.CostCenter,
		SourceIPWhitelist:  req.SourceIPWhitelist,
		ExpireAt:           expireAt,
		ContactName:        req.ContactName,
		ContactEmail:       req.ContactEmail,
		Quantity:           req.Quantity,
		ExpectedAt:         req.ExpectedAt,
	}
```

`toDTO` 函数加入（放在 `ComplianceAck` 字段后，`ExpireAt` 指针转换那段之前）：

```go
func (h *Handler) toDTO(req *ServiceRequest) *dto.ServiceRequestResponse {
	if req == nil {
		return nil
	}
	resp := &dto.ServiceRequestResponse{
		ID:                 req.ID,
		TicketID:           req.TicketID,
		CatalogID:          req.CatalogID,
		RequesterID:        req.RequesterID,
		CIID:               req.CiID,
		FormData:           req.FormData,
		CostCenter:         req.CostCenter,
		DataClassification: req.DataClassification,
		NeedsPublicIP:      req.NeedsPublicIP,
		SourceIPWhitelist:  req.SourceIPWhitelist,
		ComplianceAck:      req.ComplianceAck,
		ContactName:        req.ContactName,
		ContactEmail:       req.ContactEmail,
		Quantity:           req.Quantity,
		Version:            req.Version,
		ProcessorID:        req.ProcessorID,
		StartedAt:          req.StartedAt,
		CompletedAt:        req.CompletedAt,
		CompletionNote:     req.CompletionNote,
		LastError:          req.LastError,
		CreatedAt:          req.CreatedAt,
		UpdatedAt:          req.UpdatedAt,
		TicketTitle:        req.TicketTitle,
		TicketStatus:       req.TicketStatus,
	}
	if req.ExpireAt != nil {
		t := *req.ExpireAt
		resp.ExpireAt = &t
	}
	if req.ExpectedAt != nil {
		t := *req.ExpectedAt
		resp.ExpectedAt = &t
	}
	return resp
}
```

- [ ] **Step 8: 确认整个后端编译通过且 service_request 包测试全绿**

```bash
go build ./... && go test ./handlers/service_request/... -v 2>&1 | tail -60
```

Expected: 无编译错误，所有测试 `PASS`（包括本任务新加的两个，以及 Task 1/2 之前已有的测试不受影响）。

- [ ] **Step 9: Commit**

```bash
git add itsm-backend/handlers/service_request/entity.go itsm-backend/handlers/service_request/repository_impl.go itsm-backend/handlers/service_request/repository_impl_test.go itsm-backend/handlers/service_request/handler.go itsm-backend/dto/service_dto.go
git commit -m "feat(service-request): wire contactName/contactEmail/quantity/expectedAt end-to-end"
```

---

### Task 4: `Create()` 按 `RequiresInfraFields` 收紧强制校验

**Files:**
- Modify: `itsm-backend/handlers/service_request/service.go`
- Test: `itsm-backend/handlers/service_request/service_test.go`

**Interfaces:**
- Consumes: `service_catalog.RequiresInfraFields(serviceType string) bool`（Task 2 产出）
- Produces: 无新增导出符号，只是收紧了 `Service.Create` 内部的校验分支。

- [ ] **Step 1: 写两个失败的测试——非基础设施类型跳过校验、基础设施类型维持强校验**

打开 `itsm-backend/handlers/service_request/service_test.go`，在文件末尾追加：

```go
// TestService_Create_NonInfraCatalog_SkipsInfraValidation 证明 custom 类型的目录项
// （如 Copilot 采购申请）不需要合规确认/过期时间等基础设施字段也能提交成功——
// 这是本次要修的回归：之前所有 service_type 都被无条件要求这组字段。
func TestService_Create_NonInfraCatalog_SkipsInfraValidation(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_non_infra?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-non-infra").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("requester-non-infra").SetEmail("nireq@test.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	// service_catalog.Service.Create 不支持设置 service_type，直接用 ent client 改，
	// 默认值是 "custom"（ent schema 的 field.String("service_type")...Default("custom")）,
	// 这里显式设置一次只是让测试意图更清楚。
	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "Copilot采购申请", "基础设施", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "")
	require.NoError(t, err)
	_, err = client.ServiceCatalog.UpdateOneID(catalog.ID).SetServiceType("custom").Save(ctx)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	ticketSvc := service.NewTicketServiceForTest(client, zaptest.NewLogger(t).Sugar())
	svc := NewService(srRepo, scRepo, cmdbRepo, client, zaptest.NewLogger(t).Sugar(), ticketSvc, nil, nil)

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		// 故意不设置 ComplianceAck/ExpireAt/DataClassification/NeedsPublicIP——
		// 非基础设施类型不应该要求这些。
		FormData: map[string]interface{}{"title": "申请 Copilot 许可证", "reason": "提升研发效率"},
	})
	require.NoError(t, err, "custom 类型目录项不应该被要求填写基础设施字段")
	require.Greater(t, created.TicketID, 0)
}

// TestService_Create_InfraCatalog_StillRequiresComplianceAck 证明 vm/network/database
// 类型的目录项仍然维持原有的强制校验——本次收紧范围，不是放松安全要求。
func TestService_Create_InfraCatalog_StillRequiresComplianceAck(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_infra_still_required?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-infra-required").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("requester-infra").SetEmail("infrareq@test.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云服务器申请", "云资源", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "")
	require.NoError(t, err)
	_, err = client.ServiceCatalog.UpdateOneID(catalog.ID).SetServiceType("vm").Save(ctx)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	ticketSvc := service.NewTicketServiceForTest(client, zaptest.NewLogger(t).Sugar())
	svc := NewService(srRepo, scRepo, cmdbRepo, client, zaptest.NewLogger(t).Sugar(), ticketSvc, nil, nil)

	_, err = svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		// 故意不设置 ComplianceAck（零值 false）
		FormData: map[string]interface{}{"title": "申请一台云主机", "reason": "测试"},
	})
	require.Error(t, err, "vm 类型目录项仍然应该要求合规确认")
	require.Contains(t, err.Error(), "Compliance acknowledgement required")
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd itsm-backend && go test ./handlers/service_request/... -run "TestService_Create_NonInfraCatalog_SkipsInfraValidation|TestService_Create_InfraCatalog_StillRequiresComplianceAck" -v
```

Expected: `TestService_Create_NonInfraCatalog_SkipsInfraValidation` 失败（当前代码对 custom 类型也会要求 ComplianceAck），报错信息里能看到 `Compliance acknowledgement required`。

- [ ] **Step 3: 收紧校验分支**

打开 `itsm-backend/handlers/service_request/service.go`，把第 89-110 行的校验块：

```go
	// 2. Validate Request Data
	if !reqData.ComplianceAck {
		return nil, common.NewBadRequestError("Compliance acknowledgement required", nil)
	}
	if reqData.NeedsPublicIP && len(reqData.SourceIPWhitelist) == 0 {
		return nil, common.NewBadRequestError("Source IP whitelist required for public IP", nil)
	}
	if reqData.ExpireAt == nil {
		return nil, common.NewBadRequestError("Expiration date required", nil)
	}
	if !reqData.ExpireAt.After(time.Now()) {
		return nil, common.NewBadRequestError("Expiration date must be in the future", nil)
	}
	title := strings.TrimSpace(reqData.title())
	if title == "" {
		return nil, common.NewBadRequestError("Request title is required", nil)
	}
	switch reqData.DataClassification {
	case "public", "internal", "confidential", "restricted":
	default:
		return nil, common.NewBadRequestError("Invalid data classification", nil)
	}
```

改成：

```go
	// 2. Validate Request Data
	title := strings.TrimSpace(reqData.title())
	if title == "" {
		return nil, common.NewBadRequestError("Request title is required", nil)
	}
	// 基础设施字段组（成本中心/数据分级/公网IP/IP白名单/资源过期时间/合规确认）只对
	// vm/network/database 三类目录项强制要求——这条判断只在 service_catalog.RequiresInfraFields
	// 一处实现，见该函数注释。custom/access/security/software/devops 等类型（如 Copilot
	// 采购申请）不再被要求填写这组跟业务无关的基础设施字段。
	if service_catalog.RequiresInfraFields(cat.ServiceType) {
		if !reqData.ComplianceAck {
			return nil, common.NewBadRequestError("Compliance acknowledgement required", nil)
		}
		if reqData.NeedsPublicIP && len(reqData.SourceIPWhitelist) == 0 {
			return nil, common.NewBadRequestError("Source IP whitelist required for public IP", nil)
		}
		if reqData.ExpireAt == nil {
			return nil, common.NewBadRequestError("Expiration date required", nil)
		}
		if !reqData.ExpireAt.After(time.Now()) {
			return nil, common.NewBadRequestError("Expiration date must be in the future", nil)
		}
		switch reqData.DataClassification {
		case "public", "internal", "confidential", "restricted":
		default:
			return nil, common.NewBadRequestError("Invalid data classification", nil)
		}
	}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./handlers/service_request/... -v 2>&1 | tail -80
```

Expected: 本任务新加的两个测试 `PASS`，并且 Task 1-3 之前的所有测试仍然 `PASS`（尤其是 `TestService_Create_RequiredFieldMissing_Rejected`——它用的目录项没设置 `service_type`，走 ent schema 默认值 `"custom"`，属于非基础设施类型；它验证的是"必填自定义字段"缺失被拒绝，跟这次收紧的 4 段基础设施校验是两套独立机制，不应互相影响）。

- [ ] **Step 5: 确认整个后端编译通过**

```bash
go build ./...
```

Expected: 无输出，退出码 0。

- [ ] **Step 6: Commit**

```bash
git add itsm-backend/handlers/service_request/service.go itsm-backend/handlers/service_request/service_test.go
git commit -m "fix(service-request): only require infra fields for vm/network/database catalogs"
```

---

### Task 5: 前端 `service-catalog-api.ts` 提交载荷补上 4 个新字段

**Files:**
- Modify: `itsm-frontend/src/lib/api/service-catalog-api.ts`

**Interfaces:**
- Consumes: `dto.CreateServiceRequestRequest.contactName/contactEmail/quantity/expectedAt`（Task 3 产出的后端 DTO 字段）
- Produces: `ServiceCatalogApi.createServiceRequest` 提交的 payload 顶层多出 `contactName`/`contactEmail`/`quantity`/`expectedAt` 四个 key，供 Task 6 的表单调用。

- [ ] **Step 1: 在 `createServiceRequest` 里追加字段提取**

打开 `itsm-frontend/src/lib/api/service-catalog-api.ts`，找到 `createServiceRequest` 方法里的 `payload` 对象（"V0：最小字段集合"那段注释下面），把：

```ts
    const payload: unknown = {
      catalogId: Number(request.serviceId),
      title: title ? String(title) : undefined,
      reason,
      formData: request.formData || {},
      complianceAck: Boolean(request.formData?.complianceAck ?? true), // 以表单勾选为准，兜底为 true
      dataClassification: String(request.formData?.dataClassification || 'internal'),
      needsPublicIp: Boolean(request.formData?.needsPublicIp || false),
      sourceIpWhitelist: Array.isArray(request.formData?.sourceIpWhitelist)
        ? request.formData?.sourceIpWhitelist
        : undefined,
      costCenter: request.formData?.costCenter
        ? String(request.formData?.costCenter)
        : undefined,
      expireAt: request.formData?.expireAt ? request.formData?.expireAt : undefined,
    };
```

改成：

```ts
    const payload: unknown = {
      catalogId: Number(request.serviceId),
      title: title ? String(title) : undefined,
      reason,
      formData: request.formData || {},
      complianceAck: Boolean(request.formData?.complianceAck ?? true), // 以表单勾选为准，兜底为 true
      dataClassification: String(request.formData?.dataClassification || 'internal'),
      needsPublicIp: Boolean(request.formData?.needsPublicIp || false),
      sourceIpWhitelist: Array.isArray(request.formData?.sourceIpWhitelist)
        ? request.formData?.sourceIpWhitelist
        : undefined,
      costCenter: request.formData?.costCenter
        ? String(request.formData?.costCenter)
        : undefined,
      expireAt: request.formData?.expireAt ? request.formData?.expireAt : undefined,
      // 通用层字段：所有 service_type 都适用，真正落到后端 ContactName/ContactEmail/
      // Quantity/ExpectedAt 列，不再只是进 formData 就没人读的假字段。
      contactName: request.formData?.contactName
        ? String(request.formData?.contactName)
        : undefined,
      contactEmail: request.formData?.contactEmail
        ? String(request.formData?.contactEmail)
        : undefined,
      quantity: request.formData?.quantity ? Number(request.formData?.quantity) : undefined,
      expectedAt: request.formData?.expectedAt ? request.formData?.expectedAt : undefined,
    };
```

- [ ] **Step 2: 类型检查**

```bash
cd itsm-frontend && npm run type-check
```

Expected: 无错误（`payload` 是 `unknown` 类型，这个改动不会引入新的类型错误）。

- [ ] **Step 3: Commit**

```bash
git add itsm-frontend/src/lib/api/service-catalog-api.ts
git commit -m "feat(service-catalog): submit contactName/contactEmail/quantity/expectedAt to backend"
```

---

### Task 6: 申请表单页按 `requiresInfraFields` 分层渲染 + 联系人字段可编辑

**Files:**
- Modify: `itsm-frontend/src/app/(main)/service-catalog/request/[id]/page.tsx`

**Interfaces:**
- Consumes: `catalog.requiresInfraFields`（Task 2 后端 DTO 新字段，通过现有 `GET /api/v1/service-catalogs/:id` 直接拿到，前端已有的 `httpClient.get` 调用不需要改）
- Produces: 无新增导出，页面行为变化。

- [ ] **Step 1: 联系人字段改为真正可编辑（去掉 `disabled`），字段名对齐后端**

打开 `itsm-frontend/src/app/(main)/service-catalog/request/[id]/page.tsx`，把：

```tsx
          <div className="grid grid-cols-2 gap-4">
            <Form.Item name="requesterName" label="申请人">
              <Input disabled placeholder="当前登录用户" />
            </Form.Item>
            <Form.Item name="requesterEmail" label="联系邮箱">
              <Input disabled placeholder="当前用户邮箱" />
            </Form.Item>
          </div>
```

改成：

```tsx
          <div className="grid grid-cols-2 gap-4">
            <Form.Item
              name="contactName"
              label="联系人"
              extra="默认取当前登录用户，如代他人提交可修改"
              rules={[{ required: true, message: '请输入联系人姓名' }]}
            >
              <Input placeholder="联系人姓名" />
            </Form.Item>
            <Form.Item
              name="contactEmail"
              label="联系邮箱"
              rules={[
                { required: true, message: '请输入联系邮箱' },
                { type: 'email', message: '请输入合法的邮箱地址' },
              ]}
            >
              <Input placeholder="联系邮箱" />
            </Form.Item>
          </div>
```

对应地，把预填当前用户信息的 `useEffect`：

```tsx
  useEffect(() => {
    if (user) {
      form.setFieldsValue({ requesterName: user.name, requesterEmail: user.email });
    }
  }, [form, user]);
```

改成：

```tsx
  useEffect(() => {
    if (user) {
      form.setFieldsValue({ contactName: user.name, contactEmail: user.email });
    }
  }, [form, user]);
```

- [ ] **Step 2: `onFinish` 里把新字段加进提交的 `formData`**

把 `onFinish` 里的 `payload.formData` 对象：

```tsx
      const payload: any = {
        serviceId: id,
        formData: {
          requesterName: values.requesterName,
          requesterEmail: values.requesterEmail,
          title: values.title,
          reason: values.reason,
          quantity: values.quantity || 1,
          expectedAt: values.expectedAt ? values.expectedAt.toISOString() : undefined,
          costCenter: values.costCenter,
          dataClassification: values.dataClassification || 'internal',
          needsPublicIp: values.needsPublicIp || false,
          sourceIpWhitelist: values.sourceIpWhitelist
            ? values.sourceIpWhitelist.split(',').map((s: string) => s.trim()).filter(Boolean)
            : undefined,
          // B10: 合规确认 + 过期时间
          complianceAck: !!values.complianceAck,
          expireAt: expireAt ? expireAt.toISOString() : undefined,
          customFieldValues,
        },
      };
```

改成：

```tsx
      const payload: any = {
        serviceId: id,
        formData: {
          contactName: values.contactName,
          contactEmail: values.contactEmail,
          title: values.title,
          reason: values.reason,
          quantity: values.quantity || 1,
          expectedAt: values.expectedAt ? values.expectedAt.toISOString() : undefined,
          costCenter: values.costCenter,
          dataClassification: values.dataClassification || 'internal',
          needsPublicIp: values.needsPublicIp || false,
          sourceIpWhitelist: values.sourceIpWhitelist
            ? values.sourceIpWhitelist.split(',').map((s: string) => s.trim()).filter(Boolean)
            : undefined,
          // B10: 合规确认 + 过期时间（仅基础设施类目录项渲染，见下方 requiresInfraFields 分支）
          complianceAck: !!values.complianceAck,
          expireAt: expireAt ? expireAt.toISOString() : undefined,
          customFieldValues,
        },
      };
```

- [ ] **Step 3: 把基础设施字段组包进 `requiresInfraFields` 条件渲染**

把从"成本中心/数据分级"那两个 `Form.Item` 开始、到"合规确认"`Form.Item` 结束的这一整段：

```tsx
          <div className="grid grid-cols-2 gap-4">
            <Form.Item name="costCenter" label="成本中心">
              <Input placeholder="例如 CC-1001" />
            </Form.Item>
            <Form.Item
              name="dataClassification"
              label="数据分级"
              initialValue="internal"
            >
              <Select
                options={[
                  { label: '公开 (public)', value: 'public' },
                  { label: '内部 (internal)', value: 'internal' },
                  { label: '机密 (confidential)', value: 'confidential' },
                  { label: '绝密 (restricted)', value: 'restricted' },
                ]}
              />
            </Form.Item>
          </div>

          <Form.Item name="needsPublicIp" valuePropName="checked">
            <Checkbox>需要公网 IP</Checkbox>
          </Form.Item>

          <Form.Item
            name="sourceIpWhitelist"
            label="来源 IP 白名单（多个以英文逗号分隔）"
            dependencies={['needsPublicIp']}
          >
            <Input placeholder="例如 1.2.3.4, 10.0.0.0/8" />
          </Form.Item>

          <Divider />

          <Form.Item
            name="expireAt"
            label="资源过期时间（到期自动回收）"
            extra="若不填写，则按服务目录默认策略"
          >
            <DatePicker
              showTime
              style={{ width: '100%' }}
              disabledDate={(d) => d && d.isBefore(dayjs().startOf('day'))}
            />
          </Form.Item>

          <Form.Item
            name="complianceAck"
            valuePropName="checked"
            rules={[
              {
                validator: (_, value) =>
                  value
                    ? Promise.resolve()
                    : Promise.reject(new Error('请确认已知悉相关合规与安全要求')),
              },
            ]}
          >
            <Checkbox>
              我已知悉本服务的合规要求与安全策略，并承诺仅将资源用于申请所述的合法业务场景
            </Checkbox>
          </Form.Item>
```

改成（整段包一层 `{catalog?.requiresInfraFields && (...)}`）：

```tsx
          {catalog?.requiresInfraFields && (
            <>
              <div className="grid grid-cols-2 gap-4">
                <Form.Item name="costCenter" label="成本中心">
                  <Input placeholder="例如 CC-1001" />
                </Form.Item>
                <Form.Item
                  name="dataClassification"
                  label="数据分级"
                  initialValue="internal"
                >
                  <Select
                    options={[
                      { label: '公开 (public)', value: 'public' },
                      { label: '内部 (internal)', value: 'internal' },
                      { label: '机密 (confidential)', value: 'confidential' },
                      { label: '绝密 (restricted)', value: 'restricted' },
                    ]}
                  />
                </Form.Item>
              </div>

              <Form.Item name="needsPublicIp" valuePropName="checked">
                <Checkbox>需要公网 IP</Checkbox>
              </Form.Item>

              <Form.Item
                name="sourceIpWhitelist"
                label="来源 IP 白名单（多个以英文逗号分隔）"
                dependencies={['needsPublicIp']}
              >
                <Input placeholder="例如 1.2.3.4, 10.0.0.0/8" />
              </Form.Item>

              <Divider />

              <Form.Item
                name="expireAt"
                label="资源过期时间（到期自动回收）"
                extra="若不填写，则按服务目录默认策略"
              >
                <DatePicker
                  showTime
                  style={{ width: '100%' }}
                  disabledDate={(d) => d && d.isBefore(dayjs().startOf('day'))}
                />
              </Form.Item>

              <Form.Item
                name="complianceAck"
                valuePropName="checked"
                rules={[
                  {
                    validator: (_, value) =>
                      value
                        ? Promise.resolve()
                        : Promise.reject(new Error('请确认已知悉相关合规与安全要求')),
                  },
                ]}
              >
                <Checkbox>
                  我已知悉本服务的合规要求与安全策略，并承诺仅将资源用于申请所述的合法业务场景
                </Checkbox>
              </Form.Item>
            </>
          )}
```

- [ ] **Step 4: 类型检查**

```bash
cd itsm-frontend && npm run type-check
```

Expected: 无错误。

- [ ] **Step 5: Commit**

```bash
git add "itsm-frontend/src/app/(main)/service-catalog/request/[id]/page.tsx"
git commit -m "feat(service-catalog): gate infra fields by requiresInfraFields, make contact fields editable"
```

---

### Task 7: `ServiceRequestPanel.tsx` 展示新字段

**Files:**
- Modify: `itsm-frontend/src/components/ticket/ServiceRequestPanel.tsx`

**Interfaces:**
- Consumes: `request.contactName`/`request.contactEmail`/`request.quantity`/`request.expectedAt`（Task 3 后端响应新字段，`ServiceCatalogApi.getServiceRequestByTicketId` 用 `{...raw}` 展开透传，不需要额外适配）
- Produces: 无新增导出。

- [ ] **Step 1: 在 `Descriptions` 里加 4 行**

打开 `itsm-frontend/src/components/ticket/ServiceRequestPanel.tsx`，把：

```tsx
      <Descriptions column={2} bordered size="small">
        <Descriptions.Item label="成本中心">{request.costCenter || '-'}</Descriptions.Item>
        <Descriptions.Item label="数据分类">{request.dataClassification || 'internal'}</Descriptions.Item>
        <Descriptions.Item label="需要公网IP">{request.needsPublicIp ? '是' : '否'}</Descriptions.Item>
        <Descriptions.Item label="到期时间">
          {request.expireAt ? new Date(request.expireAt).toLocaleString() : '-'}
        </Descriptions.Item>
        <Descriptions.Item label="关联CI">
          {request.ciId ? (
            <Button type="link" onClick={() => router.push(`/cmdb/cis/${request.ciId}`)}>
              CI #{request.ciId}
            </Button>
          ) : (
            '-'
          )}
        </Descriptions.Item>
      </Descriptions>
```

改成：

```tsx
      <Descriptions column={2} bordered size="small">
        <Descriptions.Item label="联系人">{request.contactName || '-'}</Descriptions.Item>
        <Descriptions.Item label="联系邮箱">{request.contactEmail || '-'}</Descriptions.Item>
        <Descriptions.Item label="数量">{request.quantity || 1}</Descriptions.Item>
        <Descriptions.Item label="期望交付时间">
          {request.expectedAt ? new Date(request.expectedAt).toLocaleString() : '-'}
        </Descriptions.Item>
        <Descriptions.Item label="成本中心">{request.costCenter || '-'}</Descriptions.Item>
        <Descriptions.Item label="数据分类">{request.dataClassification || 'internal'}</Descriptions.Item>
        <Descriptions.Item label="需要公网IP">{request.needsPublicIp ? '是' : '否'}</Descriptions.Item>
        <Descriptions.Item label="到期时间">
          {request.expireAt ? new Date(request.expireAt).toLocaleString() : '-'}
        </Descriptions.Item>
        <Descriptions.Item label="关联CI">
          {request.ciId ? (
            <Button type="link" onClick={() => router.push(`/cmdb/cis/${request.ciId}`)}>
              CI #{request.ciId}
            </Button>
          ) : (
            '-'
          )}
        </Descriptions.Item>
      </Descriptions>
```

（保留原有 4 个基础设施字段的展示，不按 `requiresInfraFields` 隐藏——那需要额外让后端把 `Catalog` 关联对象填进 `ServiceRequestResponse`，属于当前设计文档没有承诺的范围，本次不做，只加新字段展示。）

- [ ] **Step 2: 类型检查**

```bash
cd itsm-frontend && npm run type-check
```

Expected: 无错误（`request` 是 `useState<any>`，新增字段访问不会报类型错误）。

- [ ] **Step 3: 跑现有的 Panel 测试确认没有破坏**

```bash
cd itsm-frontend && npx jest src/components/ticket/__tests__/ServiceRequestPanel.test.tsx --watchAll=false --ci
```

Expected: 全部通过。

- [ ] **Step 4: Commit**

```bash
git add itsm-frontend/src/components/ticket/ServiceRequestPanel.tsx
git commit -m "feat(service-catalog): display contactName/contactEmail/quantity/expectedAt in ServiceRequestPanel"
```

---

### Task 8: 前端条件渲染的 Jest 回归测试

**Files:**
- Create: `itsm-frontend/src/app/(main)/service-catalog/request/[id]/__tests__/page.test.tsx`

**Interfaces:**
- Consumes: Task 6 改完的 `ServiceCatalogRequestPage` 组件
- Produces: 无新增导出，只是回归测试。

- [ ] **Step 1: 写测试，先确认能跑起来（此时应该已经通过，因为 Task 6 已完成实现——这里是给这个具体行为补一个防回归测试，不是严格 TDD 先写后实现）**

创建 `itsm-frontend/src/app/(main)/service-catalog/request/[id]/__tests__/page.test.tsx`：

```tsx
import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import ServiceCatalogRequestPage from '../page';

jest.mock('next/navigation', () => ({
  useParams: () => ({ id: '24' }),
  useRouter: () => ({ push: jest.fn() }),
}));

jest.mock('@/lib/store/auth-store', () => ({
  useAuthStore: (selector: (state: any) => any) =>
    selector({ user: { name: '测试用户', email: 'test@example.com' } }),
}));

const mockGet = jest.fn();
jest.mock('@/lib/api/http-client', () => ({
  httpClient: {
    get: (...args: unknown[]) => mockGet(...args),
  },
}));

jest.mock('@/lib/api/service-catalog-api', () => ({
  ServiceCatalogApi: { createServiceRequest: jest.fn() },
}));

describe('ServiceCatalogRequestPage', () => {
  afterEach(() => {
    mockGet.mockReset();
  });

  it('不渲染基础设施字段组：requiresInfraFields=false（如 Copilot 采购申请）', async () => {
    mockGet.mockResolvedValue({
      data: {
        id: 24,
        name: 'Copilot采购申请',
        requiresInfraFields: false,
        fields: [],
      },
    });

    render(<ServiceCatalogRequestPage />);

    await waitFor(() => expect(screen.getByText('申请标题')).toBeInTheDocument());

    expect(screen.queryByText('成本中心')).not.toBeInTheDocument();
    expect(screen.queryByText('数据分级')).not.toBeInTheDocument();
    expect(screen.queryByText('需要公网 IP')).not.toBeInTheDocument();
  });

  it('渲染基础设施字段组：requiresInfraFields=true（如云服务器申请）', async () => {
    mockGet.mockResolvedValue({
      data: {
        id: 5,
        name: '云服务器申请',
        requiresInfraFields: true,
        fields: [],
      },
    });

    render(<ServiceCatalogRequestPage />);

    await waitFor(() => expect(screen.getByText('成本中心')).toBeInTheDocument());

    expect(screen.getByText('数据分级')).toBeInTheDocument();
    expect(screen.getByText('需要公网 IP')).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: 跑测试**

```bash
cd itsm-frontend && npx jest "src/app/(main)/service-catalog/request/\[id\]/__tests__/page.test.tsx" --watchAll=false --ci
```

Expected: 两个测试都 `PASS`。如果因为 mock 路径或组件内部结构对不上而失败，对照 `page.tsx` 实际的 `httpClient.get` 调用参数和 `catalog` state 结构调整 mock，不要跳过这个测试。

- [ ] **Step 3: Commit**

```bash
git add "itsm-frontend/src/app/(main)/service-catalog/request/[id]/__tests__/page.test.tsx"
git commit -m "test(service-catalog): cover requiresInfraFields conditional rendering"
```

---

### Task 9: 端到端手工验证（真实浏览器提交路径）

**Files:** 无代码改动，纯验证任务。

**Interfaces:**
- Consumes: Task 1-8 的全部改动
- Produces: 验证结论（写进最终交付说明，不是代码）

- [ ] **Step 1: 重启前后端，确认拿到最新代码**

```bash
cd /home/administrator/project/itsm && ./scripts/deploy-dev.sh restart --local
```

如果脚本又因为端口 3000/3010 不一致报错（已知的 deploy-dev.sh 脚本 bug），改用手动方式：杀掉旧的 `itsm-backend-dev` 和 `next dev --port 3010` 进程，分别用 `go build` 产物和 `next dev --port 3010` 重新拉起，参考本次会话之前"帮我重启backend & frontend"时用过的做法。

- [ ] **Step 2: 用 Playwright 登录**

用 `mcp__playwright__browser_navigate` 打开 `http://localhost:3010/login`，用 admin/admin123 登录（参照本次会话之前已经跑通的登录步骤）。

- [ ] **Step 3: 验证非基础设施类目录项（Copilot 采购申请，id=24）**

导航到 `http://localhost:3010/service-catalog/request/24`，用 `browser_snapshot` 确认：
- 页面上**没有**"成本中心"、"数据分级"、"需要公网 IP"、"来源 IP 白名单"、"资源过期时间"、"合规确认"这些字段
- "联系人"、"联系邮箱"字段存在且**可编辑**（不是灰色 disabled 状态），并且已经预填了当前登录用户的姓名/邮箱

填写"申请标题"和"申请理由"（必填项），提交表单，确认跳转到了 `/tickets/:id` 且没有报错。

- [ ] **Step 4: 用真实 SQL 验证提交的字段真的落库了（不是又进了 form_data 就没人读）**

记下上一步创建的 ticket ID，然后：

```bash
PGPASSWORD=dev123 psql -h localhost -p 5432 -U itsm_user -d itsm -c \
  "SELECT contact_name, contact_email, quantity, expected_at, compliance_ack, expire_at FROM service_requests WHERE ticket_id = <上一步的 ticket id>;"
```

Expected: `contact_name`/`contact_email` 是刚才登录用户的姓名/邮箱，`quantity` 是 1（或表单里填的值），`compliance_ack` 是 `false`（因为这个类型的表单没有渲染合规确认勾选框，也没有强制要求），`expire_at` 是 `NULL`。

- [ ] **Step 5: 验证基础设施类目录项（云服务器申请，id=5 或 19）**

导航到 `http://localhost:3010/service-catalog/request/5`，确认：
- "成本中心"、"数据分级"、"需要公网 IP"、"来源 IP 白名单"、"资源过期时间"、"合规确认"这些字段**都渲染出来了**
- 不勾选"合规确认"直接提交，确认表单前端校验拦下来了（不发请求），报错提示"请确认已知悉相关合规与安全要求"
- 勾选合规确认后正常提交，确认能成功创建

- [ ] **Step 6: 打开刚创建的两个工单详情页，确认 `ServiceRequestPanel` 正确展示新字段**

分别导航到 Step 3 和 Step 5 创建的两个 ticket 详情页，用 `browser_snapshot` 确认"服务申请信息"卡片里能看到"联系人"、"联系邮箱"、"数量"、"期望交付时间"这几行，值跟提交时填的一致。

- [ ] **Step 7: 记录验证结论**

在给用户的最终交付说明里明确写清楚：本次验证走的是真实前端 http-client 提交（浏览器实际操作），不是 curl 直连后端；覆盖了 custom 和 vm 两种 `service_type`；确认了字段真实落库（用 SQL 直接查表，不是只看接口返回）。

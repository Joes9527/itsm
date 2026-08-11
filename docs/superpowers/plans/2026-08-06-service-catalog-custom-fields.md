# 服务目录自定义字段统一 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把自定义字段能力从孤立无路由的 `ServiceCatalogItem` 分支迁移到真正有路由、有 UI 的 `handlers/service_catalog`（管理员定义）+ `handlers/service_request`（员工提交值）上，复用已有的 `field_definitions`/`field_values` 表，前端补齐字段编辑器 UI，最后废弃 `ServiceCatalogItem` 及其孤立的 legacy service 文件。

**Architecture:** 不新建 Ent schema。`FieldDefinitionService`/`FieldValueService`（`itsm-backend/service/field_definition_service.go`、`field_value_service.go`）是跨领域共享基础设施，本次新增两个 `entity_type` 取值：`"service_catalog"`（字段定义，`entity_id`=`ServiceCatalog.ID`）、`"service_request"`（字段值，`entity_id`=`ServiceRequest.ID`）。前端抽一个共享的 `CustomFieldsEditor` 组件，工单模板编辑页和服务目录编辑页共用。

**Tech Stack:** Go + Gin + Ent ORM + PostgreSQL；Next.js + TypeScript + Ant Design 6。

参考设计文档：`docs/superpowers/specs/2026-08-06-service-catalog-custom-fields-design.md`

## Global Constraints

- 后端 DTO 响应字段一律 camelCase，Ent Schema 字段一律 snake_case，Mapper 负责转换（CLAUDE.md 硬性规则）。
- Controller/Handler 不得直接返回领域模型，必须走 DTO。
- 新查询必须带 `tenantID` 过滤（租户隔离硬性规则），本次每个新增的 repository/service 方法都要有对应的跨租户隔离测试。
- `field_values` 的写入沿用 `CreateTicket` 已验证的模式：主记录创建成功后单独调用 `CreateValues`，失败只记 `Warnw`、不回滚主记录——不引入"整体事务回滚"这种没有先例的新语义（详见设计文档"事务边界修正"一节）。
- `ServiceRequest.form_data` 的系统已知字段清单（`title`/`reason`/`cost_center`/`data_classification`/`source_ip_whitelist`/`expire_at`/`compliance_ack`）保持原样不动，本次只收编字段定义里明确声明过的动态字段。
- 每个 Task 完成后运行 `cd itsm-backend && gofmt -l .`（必须无输出）+ 该 Task 触及包的 `go test`；前端改动跑 `cd itsm-frontend && npx tsc --noEmit`。

---

### Task 1: `handlers/service_catalog` 接入字段定义——数据结构 + Create/Update

**Files:**
- Modify: `itsm-backend/handlers/service_catalog/entity.go`
- Modify: `itsm-backend/handlers/service_catalog/service.go`
- Modify: `itsm-backend/dto/service_dto.go`
- Modify: `itsm-backend/internal/bootstrap/app.go:495`（`NewService` 调用点补 `client` 参数）
- Modify: `itsm-backend/handlers/service_catalog/handler_test.go:83`（`scSetup` 里的 `NewService` 调用点）
- Modify: `itsm-backend/handlers/service_request/handler_test.go:96`（`srSetup` 里的 `service_catalog.NewService` 调用点）
- Modify: `itsm-backend/handlers/service_request/handler_bpmn_bridge_test.go:42`（同上）
- Modify: `itsm-backend/service/ticket_service.go:2008,2055,2084`（`toFieldDefinitionInputs` 导出为 `ToFieldDefinitionInputs`，避免和 `handlers/service_catalog` 各自维护一份同样的转换逻辑）
- Modify: `itsm-backend/service/service_catalog_item_service.go:73,200`（跟随上面的改名同步更新调用点；此文件会在 Task 10 整体删除，这里只是保持中间状态可编译）
- Test: `itsm-backend/handlers/service_catalog/service_test.go`（新建）

**Interfaces:**
- Consumes: `service.FieldDefinitionInput{Name,Label,FieldType,Required,Options,SortOrder}`、`service.NewFieldDefinitionService(client).ReplaceDefinitions(ctx, tenantID, entityType string, entityID int, defs []service.FieldDefinitionInput) ([]*ent.FieldDefinition, error)`（已存在，`itsm-backend/service/field_definition_service.go:36`）、`service.ToFieldDefinitionInputs(fields []map[string]interface{}) []service.FieldDefinitionInput`（本 Task Step 5a 把它从私有函数改成导出，供 `handlers/service_catalog` 复用，不新增重复实现）
- Produces：`Service.Create`/`Service.Update` 新增 `fields []service.FieldDefinitionInput` 参数；`ServiceCatalog` struct 新增 `Fields []service.FieldDefinitionInput` 字段，供 Task 2 的 `Get`/`List` 读取路径使用。

- [ ] **Step 1: `entity.go` 加 `Fields` 字段**

```go
// itsm-backend/handlers/service_catalog/entity.go
package service_catalog

import (
	"context"
	"time"

	"itsm-backend/service"
)

// ServiceCatalog represents the core domain entity
type ServiceCatalog struct {
	ID             int
	Name           string
	Category       string
	Description    string
	DeliveryTime   int
	CITypeID       int
	CloudServiceID int
	Status         string
	TenantID       int
	Fields         []service.FieldDefinitionInput
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
```

（`Repository` 接口、`ListFilters`、`ServiceStats` 不变，`Fields` 不经过 `Repository`——它是独立于 `service_catalogs` 表主 CRUD 的另一条写路径，由 `Service` 层直接调 `FieldDefinitionService`。）

- [ ] **Step 2: `dto/service_dto.go` 请求/响应 DTO 加 `fields`**

在 `CreateServiceCatalogRequest`（231行）和 `UpdateServiceCatalogRequest`（242行）里各加一行：

```go
	Fields []map[string]interface{} `json:"fields,omitempty"`
```

在 `ServiceCatalogResponse`（70行）里加：

```go
	Fields []map[string]interface{} `json:"fields,omitempty"`
```

- [ ] **Step 3: `service.go` 的 `Service` struct 加 `client`，`Create`/`Update` 接入 `ReplaceDefinitions`**

```go
// itsm-backend/handlers/service_catalog/service.go
package service_catalog

import (
	"context"
	"strings"

	"go.uber.org/zap"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/service"
)

// Service defines the business logic
type Service struct {
	repo   Repository
	client *ent.Client
	logger *zap.SugaredLogger
}

// NewService creates a new Service
func NewService(repo Repository, client *ent.Client, logger *zap.SugaredLogger) *Service {
	return &Service{
		repo:   repo,
		client: client,
		logger: logger,
	}
}
```

`Create` 签名加一个 `fields []service.FieldDefinitionInput` 参数（保持现有全部位置参数顺序不变，追加到末尾），在 `s.repo.Create(ctx, catalog)` 成功后接入：

```go
func (s *Service) Create(ctx context.Context, name, category, description string, deliveryTime, tenantID int, status string, ciTypeID, cloudServiceID int, fields []service.FieldDefinitionInput) (*ServiceCatalog, error) {
	// ...原有校验逻辑不变...
	catalog := &ServiceCatalog{
		Name:           name,
		Category:       category,
		Description:    description,
		DeliveryTime:   deliveryTime,
		CITypeID:       ciTypeID,
		CloudServiceID: cloudServiceID,
		Status:         status,
		TenantID:       tenantID,
	}
	created, err := s.repo.Create(ctx, catalog)
	if err != nil {
		return nil, err
	}
	if s.client != nil {
		if _, err := service.NewFieldDefinitionService(s.client).ReplaceDefinitions(ctx, tenantID, "service_catalog", created.ID, fields); err != nil {
			return nil, common.NewInternalError("Failed to save custom field definitions", err)
		}
	}
	created.Fields = fields
	return created, nil
}
```

`Update` 同样加 `fields []service.FieldDefinitionInput` 参数，在 `s.repo.Update(ctx, tenantID, current)` 成功后接入同样的 `ReplaceDefinitions` 调用（`entityID` 用 `id`），`fields` 为 `nil` 时跳过（沿用工单模板 `UpdateTemplate` 的"nil 不动、非 nil 才替换"语义——参考 `itsm-backend/service/ticket_template_service.go` 里 `if req.Fields != nil { ReplaceDefinitions... }` 那段）：

```go
func (s *Service) Update(ctx context.Context, tenantID int, id int, name, category, description string, deliveryTime int, status string, ciTypeID, cloudServiceID int, fields []service.FieldDefinitionInput) (*ServiceCatalog, error) {
	// ...原有逻辑不变，到 s.repo.Update 那行为止...
	updated, err := s.repo.Update(ctx, tenantID, current)
	if err != nil {
		return nil, err
	}
	if fields != nil && s.client != nil {
		if _, err := service.NewFieldDefinitionService(s.client).ReplaceDefinitions(ctx, tenantID, "service_catalog", id, fields); err != nil {
			return nil, common.NewInternalError("Failed to save custom field definitions", err)
		}
		updated.Fields = fields
	}
	return updated, nil
}
```

- [ ] **Step 4: `internal/bootstrap/app.go:495` 更新调用点**

```go
	scService := service_catalog.NewService(scRepo, client, sugar)
```

（`client` 变量在该行所在函数作用域内已存在，第505行 `service_request.NewService(srRepo, scRepo, cmdbRepo, client, sugar)` 已经在用同一个 `client`。）

- [ ] **Step 5a: 先把 `service` 包里已有的同名转换函数导出，而不是跨包复制一份**

`itsm-backend/service/ticket_service.go:2008` 已经有一个逻辑完全一样的函数 `toFieldDefinitionInputs（fields []map[string]interface{}) []FieldDefinitionInput`，只是没导出，`handlers/service_catalog` 这种外部包用不了。把它改成导出：

```go
// itsm-backend/service/ticket_service.go:2008
func ToFieldDefinitionInputs(fields []map[string]interface{}) []FieldDefinitionInput {
```

同一个文件里两处调用点（2055、2084行）改成大写：

```go
		Fields:        ToFieldDefinitionInputs(createReq.Fields),
```

```go
		fields = ToFieldDefinitionInputs(updateReq.Fields)
```

`itsm-backend/service/service_catalog_item_service.go` 里还有两处调用（73、200行，这个文件本身会在 Task 10 整体删除，但在那之前的每个 Task 都要保持能编译）同样改成大写：

```go
	if _, err := NewFieldDefinitionService(s.client).ReplaceDefinitions(ctx, tenantID, "service_catalog_item", item.ID, ToFieldDefinitionInputs(req.Fields)); err != nil {
```

```go
		if _, err := NewFieldDefinitionService(s.client).ReplaceDefinitions(ctx, tenantID, "service_catalog_item", id, ToFieldDefinitionInputs(*req.Fields)); err != nil {
```

Run: `cd itsm-backend && go build ./service/... 2>&1 | tail -30`
Expected: 无输出（确认改名没有漏掉调用点）

- [ ] **Step 5b: `handler.go` 的 `Create`/`Update`/`toDTO` 接入 `fields`**

`Create`（117行起）：`ShouldBindJSON` 拿到 `req.Fields`（`[]map[string]interface{}`）后转换，直接调用 Step 5a 导出的函数，不新增任何包级辅助函数：

```go
	fields := service.ToFieldDefinitionInputs(req.Fields)
```

`h.service.Create(...)` 调用末尾加 `fields` 实参；`Update` 同理。`toDTO`（308行）加：

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
		ID:             c.ID,
		Name:           c.Name,
		Category:       c.Category,
		Description:    c.Description,
		DeliveryTime:   strconv.Itoa(c.DeliveryTime),
		CITypeID:       c.CITypeID,
		CloudServiceID: c.CloudServiceID,
		Status:         c.Status,
		Fields:         fields,
		CreatedAt:      c.CreatedAt,
		UpdatedAt:      c.UpdatedAt,
	}
}
```

（`handler.go` 顶部 `import` 加 `"itsm-backend/service"`。）

- [ ] **Step 6: 更新其它受这次签名变更影响的调用点**

两处签名都变了，全仓库分别搜一遍，`go build ./...` 默认不编译 `_test.go`，这些不会在下一步之前被发现：

**`NewService` 从 `(repo, logger)` 改成 `(repo, client, logger)`：**

`itsm-backend/handlers/service_catalog/handler_test.go:83`：

```go
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())
```

`itsm-backend/handlers/service_request/handler_test.go:96`（`srSetup` 函数内）：

```go
	scSvc := service_catalog.NewService(scRepo, client, logger)
```

`itsm-backend/handlers/service_request/handler_bpmn_bridge_test.go:42`：

```go
	scSvc := service_catalog.NewService(scRepo, client, logger)
```

（这三处 `client`/`logger` 变量在各自函数作用域内都已经存在，只是原来没传给 `NewService`。）

**`Service.Create` 新增了末尾的 `fields []service.FieldDefinitionInput` 参数**，以下 3 处直接调用（不经过 HTTP handler，所以 Step 5 改 `handler.go` 覆盖不到）要在调用末尾加一个 `nil` 实参：

`itsm-backend/handlers/service_request/handler_test.go:97` 和 `:340`，以及 `itsm-backend/handlers/service_request/handler_bpmn_bridge_test.go:43`，三处都是同一行代码：

```go
	cat, err := scSvc.Create(ctx, "SRCatalog-"+srUID(), "software", "for test", 0, tenant.ID, "enabled", 0, 0, nil)
```

- [ ] **Step 7: 编译 + 跑受影响包的测试确认**

Run: `cd itsm-backend && go build ./... && go vet ./... 2>&1 | tail -60`
Expected: 无输出

Run: `cd itsm-backend && go test ./handlers/service_catalog/... ./handlers/service_request/... -v 2>&1 | tail -100`
Expected: 全部 PASS（这一步会真正编译到 Step 6 改的三个 `_test.go` 文件，验证签名改动没有遗漏调用点）

- [ ] **Step 8: Commit**

```bash
cd itsm-backend
gofmt -l .
git add handlers/service_catalog/entity.go handlers/service_catalog/service.go handlers/service_catalog/handler.go handlers/service_catalog/handler_test.go handlers/service_catalog/service_test.go dto/service_dto.go internal/bootstrap/app.go handlers/service_request/handler_test.go handlers/service_request/handler_bpmn_bridge_test.go service/ticket_service.go service/service_catalog_item_service.go
git commit -m "feat(backend): wire field_definitions into handlers/service_catalog Create/Update"
```

---

### Task 2: `handlers/service_catalog` 的 `Get`/`List` 读取字段定义（List 走批量，避免 N+1）

**Files:**
- Modify: `itsm-backend/handlers/service_catalog/service.go`
- Modify: `itsm-backend/handlers/service_catalog/handler.go`
- Test: `itsm-backend/handlers/service_catalog/service_test.go`

**Interfaces:**
- Consumes: `service.NewFieldDefinitionService(client).ListDefinitions(ctx, tenantID, entityType string, entityID int) ([]*ent.FieldDefinition, error)`；`ListDefinitionsForEntities(ctx, tenantID, entityType string, entityIDs []int) (map[int][]*ent.FieldDefinition, error)`（均已存在，`itsm-backend/service/field_definition_service.go:87,99`）
- Produces：`Service.Get` 返回的 `*ServiceCatalog.Fields` 已填充；`Service.List` 返回的每个 `*ServiceCatalog.Fields` 已填充，且整个 List 调用只查一次 `field_definitions`（不是每条目录各查一次）。

- [ ] **Step 1: 写失败测试，断言 `Get` 返回字段定义、`List` 不产生 N+1**

```go
// itsm-backend/handlers/service_catalog/service_test.go
package service_catalog

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"
	"itsm-backend/service"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestService_CreateAndGet_PersistsFieldDefinitions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sc-fields").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	created, err := svc.Create(ctx, "云主机申请", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{{Name: "environment", Label: "环境", FieldType: "text", Required: true}})
	require.NoError(t, err)
	require.Len(t, created.Fields, 1)
	assert.Equal(t, "environment", created.Fields[0].Name)

	fetched, err := svc.Get(ctx, tenant.ID, created.ID)
	require.NoError(t, err)
	require.Len(t, fetched.Fields, 1)
	assert.Equal(t, "环境", fetched.Fields[0].Label)
}

func TestService_List_BatchLoadsFieldDefinitionsPerCatalog(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_list_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sc-list-fields").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	c1, err := svc.Create(ctx, "云主机申请", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{{Name: "environment", Label: "环境", FieldType: "text"}})
	require.NoError(t, err)
	c2, err := svc.Create(ctx, "VPN权限", "网络", "desc", 1, tenant.ID, "enabled", 0, 0, nil)
	require.NoError(t, err)

	list, _, err := svc.List(ctx, tenant.ID, ListFilters{Page: 1, Size: 10})
	require.NoError(t, err)
	byID := map[int]*ServiceCatalog{}
	for _, c := range list {
		byID[c.ID] = c
	}
	require.Len(t, byID[c1.ID].Fields, 1)
	assert.Equal(t, "environment", byID[c1.ID].Fields[0].Name)
	assert.Empty(t, byID[c2.ID].Fields)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd itsm-backend && go test ./handlers/service_catalog/... -run TestService_CreateAndGet_PersistsFieldDefinitions -v`
Expected: FAIL（`Get` 目前不查字段定义，`Fields` 始终为空）

- [ ] **Step 3: `service.go` 的 `Get`/`List` 接入读取**

```go
func (s *Service) Get(ctx context.Context, tenantID int, id int) (*ServiceCatalog, error) {
	catalog, err := s.repo.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if s.client != nil {
		defs, err := service.NewFieldDefinitionService(s.client).ListDefinitions(ctx, tenantID, "service_catalog", catalog.ID)
		if err != nil {
			s.logger.Warnw("Failed to load field definitions for service catalog", "error", err, "catalog_id", catalog.ID)
		} else {
			catalog.Fields = toFieldDefinitionInputsFromEnt(defs)
		}
	}
	return catalog, nil
}

func (s *Service) List(ctx context.Context, tenantID int, filters ListFilters) ([]*ServiceCatalog, int, error) {
	if filters.Page < 1 {
		filters.Page = 1
	}
	if filters.Size < 1 {
		filters.Size = 10
	}
	if filters.Size > 100 {
		filters.Size = 100
	}
	catalogs, total, err := s.repo.List(ctx, tenantID, filters)
	if err != nil {
		return nil, 0, err
	}
	if s.client != nil && len(catalogs) > 0 {
		ids := make([]int, len(catalogs))
		for i, c := range catalogs {
			ids[i] = c.ID
		}
		defsByCatalog, err := service.NewFieldDefinitionService(s.client).ListDefinitionsForEntities(ctx, tenantID, "service_catalog", ids)
		if err != nil {
			s.logger.Warnw("Failed to batch-load field definitions for service catalogs", "error", err)
		} else {
			for _, c := range catalogs {
				c.Fields = toFieldDefinitionInputsFromEnt(defsByCatalog[c.ID])
			}
		}
	}
	return catalogs, total, nil
}

// toFieldDefinitionInputsFromEnt 把查出来的 ent.FieldDefinition 转成领域层的 FieldDefinitionInput。
func toFieldDefinitionInputsFromEnt(defs []*ent.FieldDefinition) []service.FieldDefinitionInput {
	result := make([]service.FieldDefinitionInput, 0, len(defs))
	for _, d := range defs {
		result = append(result, service.FieldDefinitionInput{
			Name: d.Name, Label: d.Label, FieldType: d.FieldType,
			Required: d.Required, Options: d.Options, SortOrder: d.SortOrder,
		})
	}
	return result
}
```

（`service.go` 顶部 `import` 加 `"itsm-backend/ent"`。）

- [ ] **Step 4: 跑测试确认通过**

Run: `cd itsm-backend && go test ./handlers/service_catalog/... -run "TestService_CreateAndGet_PersistsFieldDefinitions|TestService_List_BatchLoadsFieldDefinitionsPerCatalog" -v`
Expected: 两个都 PASS

- [ ] **Step 5: 补跨租户隔离测试**

```go
func TestService_Get_TenantIsolation_NoCrossTenantFieldLeak(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_tenant_isolation?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenantA, err := client.Tenant.Create().SetName("A").SetCode("sc-tenant-a").SetDomain("a.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	tenantB, err := client.Tenant.Create().SetName("B").SetCode("sc-tenant-b").SetDomain("b.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	catalogA, err := svc.Create(ctx, "服务A", "分类", "desc", 1, tenantA.ID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{{Name: "secretField", Label: "租户A专属字段", FieldType: "text"}})
	require.NoError(t, err)

	// 租户 B 用同样的 entity_id（碰巧撞上租户 A 的 catalog.ID）查询，不应该看到租户 A 的字段定义。
	// 用直接调 FieldDefinitionService 而非 svc.Get 来模拟"entity_id 相同、tenant_id 不同"这种最容易漏查 tenantID 的场景。
	defs, err := service.NewFieldDefinitionService(client).ListDefinitions(ctx, tenantB.ID, "service_catalog", catalogA.ID)
	require.NoError(t, err)
	assert.Empty(t, defs, "租户 B 不应该查到租户 A 的字段定义，即使 entity_id 相同")
}
```

- [ ] **Step 6: 跑全部测试确认通过**

Run: `cd itsm-backend && go test ./handlers/service_catalog/... -v 2>&1 | tail -60`
Expected: 全部 PASS

- [ ] **Step 7: Commit**

```bash
cd itsm-backend
gofmt -l .
git add handlers/service_catalog/service.go handlers/service_catalog/service_test.go
git commit -m "feat(backend): batch-load field definitions in service_catalog Get/List, add tenant isolation test"
```

---

### Task 3: `handlers/service_request` 的 `Create` 写入 `field_values`

**Files:**
- Modify: `itsm-backend/handlers/service_request/service.go`
- Test: `itsm-backend/handlers/service_request/service_test.go`（新建——该目录目前只有 `handler_test.go`/`handler_bpmn_bridge_test.go`，两者都走 HTTP handler，没有直接测 `Service` 层的文件）

**Interfaces:**
- Consumes: `service.NewFieldValueService(client).CreateValues(ctx, tenantID, defEntityType, defEntityID string/int, valueEntityType string, valueEntityID int, values map[string]interface{}) error`（已存在，`itsm-backend/service/field_value_service.go:26`）
- Produces：`Service.Create` 成功返回的 `*ServiceRequest` 不变（不新增字段——字段值走单独的 `field_values` 表，详情读取在 Task 4 做），但副作用是：`FormData` 里除系统已知字段外、且在该 `catalog_id` 的 `field_definitions` 里有定义的键，会被写入 `field_values`。

- [ ] **Step 1: 新建测试文件，参考 Task 2 `service_catalog/service_test.go` 的写法**

`enttest.Open` + `client.Tenant.Create()`；另外要创建一个 requester `User`（`Create` 方法开头会校验 `s.repo.GetUserContext(ctx, requesterID, tenantID)`，需要真实存在的用户）以及一个 `enabled` 状态的 `ServiceCatalog`（`Create` 会校验 `cat.Status == "enabled"`）。

- [ ] **Step 2: 写失败测试**

```go
func TestService_Create_PersistsFieldValues(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_field_values?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-field-values").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("requester").SetEmail("requester@test.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云主机申请", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{{Name: "environment", Label: "环境", FieldType: "text"}})
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	svc := NewService(srRepo, scRepo, cmdbRepo, client, zaptest.NewLogger(t).Sugar())

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		Title:              "申请一台云主机",
		Reason:             "测试",
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData: map[string]interface{}{
			"environment": "production",
		},
	})
	require.NoError(t, err)

	values, err := service.NewFieldValueService(client).ListValues(ctx, tenant.ID, "service_request", created.ID)
	require.NoError(t, err)
	require.Len(t, values, 1)
	assert.Equal(t, "environment", values[0].Name)
	assert.Equal(t, "production", values[0].Value)
}

func ptrTime(t time.Time) *time.Time { return &t }
```

（`import` 需要 `itsm-backend/handlers/service_catalog`、`itsm-backend/handlers/cmdb`、`"itsm-backend/service"`、`time`——跟随该文件已有的 import 风格调整别名。）

- [ ] **Step 3: 跑测试确认失败**

Run: `cd itsm-backend && go test ./handlers/service_request/... -run TestService_Create_PersistsFieldValues -v`
Expected: FAIL（`field_values` 里查不到任何记录）

- [ ] **Step 4: `service.go` 的 `Service` struct 加 `client`，`Create` 接入写值**

```go
type Service struct {
	repo           Repository
	scRepo         service_catalog.Repository
	cmdbRepo       cmdb.Repository
	client         *ent.Client
	logger         *zap.SugaredLogger
	approvalBridge *service.BPMNApprovalBridge
}

func NewService(repo Repository, scRepo service_catalog.Repository, cmdbRepo cmdb.Repository, entClient *ent.Client, logger *zap.SugaredLogger) *Service {
	svc := &Service{
		repo:     repo,
		scRepo:   scRepo,
		cmdbRepo: cmdbRepo,
		client:   entClient,
		logger:   logger,
	}
	if entClient != nil {
		svc.approvalBridge = service.NewBPMNApprovalBridge(entClient, logger)
	}
	return svc
}
```

`Create` 里 `created, err := s.repo.Create(ctx, newReq, approvals)` 之后（原有的 `if err != nil {...}` 块之后、`return created, nil` 之前）插入：

```go
	if s.client != nil {
		if fieldValues := extractServiceRequestFieldValues(reqData.FormData); len(fieldValues) > 0 {
			if err := service.NewFieldValueService(s.client).CreateValues(ctx, tenantID, "service_catalog", catalogID, "service_request", created.ID, fieldValues); err != nil {
				s.logger.Warnw("Failed to persist service request custom field values", "error", err, "service_request_id", created.ID)
			}
		}
	}
```

在文件底部加提取函数——排除掉 `handler.go` 的 `normalizeCreateServiceRequest` 已经摘走的系统已知字段：

```go
// serviceRequestSystemFormDataKeys 是 handler.go normalizeCreateServiceRequest 已经从
// FormData 摘出、写进 ServiceRequest 专用列的系统已知键。这些键即使恰好跟某个字段定义
// 同名，也不应该被当成动态自定义字段再收编一次进 field_values。
var serviceRequestSystemFormDataKeys = map[string]bool{
	"title": true, "reason": true, "cost_center": true,
	"data_classification": true, "source_ip_whitelist": true,
	"expire_at": true, "compliance_ack": true,
}

func extractServiceRequestFieldValues(formData map[string]interface{}) map[string]interface{} {
	if formData == nil {
		return nil
	}
	result := make(map[string]interface{}, len(formData))
	for k, v := range formData {
		if serviceRequestSystemFormDataKeys[k] {
			continue
		}
		result[k] = v
	}
	return result
}
```

（`CreateValues` 内部本来就会用查出来的 `field_definitions` 过滤 `values` 里不匹配的 key，见 `field_value_service.go:47` `raw, ok := values[def.Name]; if !ok { continue }`——所以这里传全量非系统字段也是安全的，不需要先查一遍 `field_definitions` 再手动比对。）

- [ ] **Step 5: 跑测试确认通过**

Run: `cd itsm-backend && go test ./handlers/service_request/... -run TestService_Create_PersistsFieldValues -v`
Expected: PASS

- [ ] **Step 6: 补一条"系统字段不被误收编"的回归测试**

```go
func TestService_Create_SystemFormDataFieldsNotCollectedAsCustomFields(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_system_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-system-fields").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("requester2").SetEmail("requester2@test.com").SetName("Requester2").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	// 故意不定义任何 field_definitions，只提交系统已知字段。
	catalog, err := scService.Create(ctx, "VPN权限", "网络", "desc", 1, tenant.ID, "enabled", 0, 0, nil)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	svc := NewService(srRepo, scRepo, cmdbRepo, client, zaptest.NewLogger(t).Sugar())

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		Title: "VPN 权限申请", Reason: "测试", ComplianceAck: true,
		DataClassification: "internal", ExpireAt: ptrTime(time.Now().Add(24 * time.Hour)),
		FormData: map[string]interface{}{"title": "不应该被当成自定义字段", "cost_center": "CC-001"},
	})
	require.NoError(t, err)

	values, err := service.NewFieldValueService(client).ListValues(ctx, tenant.ID, "service_request", created.ID)
	require.NoError(t, err)
	assert.Empty(t, values, "没有对应 field_definitions 的系统字段不应该落进 field_values")
}
```

Run: `cd itsm-backend && go test ./handlers/service_request/... -run TestService_Create_SystemFormDataFieldsNotCollectedAsCustomFields -v`
Expected: PASS（`CreateValues` 内部按 `field_definitions` 过滤，`title`/`cost_center` 没有对应定义，天然不会落库；这条测试钉住这个行为不被后续修改破坏）

- [ ] **Step 7: Commit**

```bash
cd itsm-backend
gofmt -l .
git add handlers/service_request/service.go handlers/service_request/service_test.go
git commit -m "feat(backend): persist service request custom field values on create"
```

---

### Task 4: `handlers/service_request` 详情响应加 `customFields`

**Files:**
- Modify: `itsm-backend/dto/service_dto.go`
- Modify: `itsm-backend/handlers/service_request/handler.go`
- Test: `itsm-backend/handlers/service_request/handler_test.go`

**Interfaces:**
- Consumes: `service.NewFieldValueService(client).ListValues(ctx, tenantID, entityType string, entityID int) ([]service.FieldValueDTO, error)`（已存在，`field_value_service.go:124`；`FieldValueDTO` 有 `Name`/`Label`/`Value` 字段）
- Produces：`dto.ServiceRequestResponse` 新增 `CustomFields []CustomFieldValueResponse`；`Handler.Get`（详情接口）填充这个字段，`Handler.List`（列表接口）不填充，维持跟工单一致的"列表不查字段值避免 N+1"设计。

- [ ] **Step 1: 看一眼 `toDTO` 现状 + `Get`/`List` handler 结构**

Run: `grep -n "func (h \*Handler) toDTO\|func (h \*Handler) Get\|func (h \*Handler) List" itsm-backend/handlers/service_request/handler.go`

`toDTO(req *ServiceRequest, approvals []*ServiceRequestApproval) *dto.ServiceRequestResponse` 这个签名在 `List`/`Get` 两处都会被调用（見前文 handler.go 61/145/340 行），本次要让它能区分"要不要带 customFields"——加一个参数，而不是让 `toDTO` 自己去查（保持跟工单一致的"list/detail 两个变体"设计，不是靠一个 bool 参数控制副作用查询，参考 `itsm-backend/service/ticket_service.go` 的 `ToTicketResponse`/`ToTicketResponseWithCustomFields` 拆分方式）。

- [ ] **Step 2: `dto/service_dto.go` 加字段**

在 `ServiceRequestResponse`（83行）里加：

```go
	CustomFields []CustomFieldValueResponse `json:"customFields,omitempty"`
```

`CustomFieldValueResponse` 已经在 `itsm-backend/dto/ticket_dto.go` 定义过（`{Name,Label,Value}`），直接复用，不重新定义一份。

- [ ] **Step 3: 写失败测试**

```go
// itsm-backend/handlers/service_request/handler_test.go 新增
func TestHandler_Get_IncludesCustomFieldValues(t *testing.T) {
	// srSetup 返回 (r, client, tenantID, userID, catalogID)；这里不用它预置的那个
	// catalogID（没有字段定义），另外建一个带字段的 ServiceCatalog。
	r, client, tenantID, _, _ := srSetup(t)
	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(context.Background(), "云主机申请-"+srUID(), "software", "desc", 1, tenantID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{{Name: "environment", Label: "环境", FieldType: "text"}})
	require.NoError(t, err)

	createReq := dto.CreateServiceRequestRequest{
		CatalogID: catalog.ID, Title: "申请", Reason: "测试", ComplianceAck: true,
		FormData: map[string]interface{}{"environment": "staging"},
	}
	createResp := srDoReq(t, r, "POST", "/api/v1/service-requests", createReq)
	require.Equal(t, common.SuccessCode, createResp.Code, "body=%s", srStr(createResp))
	created := createResp.Data.(map[string]interface{})
	id := int(created["id"].(float64))

	getResp := srDoReq(t, r, "GET", "/api/v1/service-requests/"+strconv.Itoa(id), nil)
	require.Equal(t, common.SuccessCode, getResp.Code, "body=%s", srStr(getResp))
	data := getResp.Data.(map[string]interface{})
	customFields := data["customFields"].([]interface{})
	require.Len(t, customFields, 1)
	first := customFields[0].(map[string]interface{})
	assert.Equal(t, "environment", first["name"])
	assert.Equal(t, "环境", first["label"])
	assert.Equal(t, "staging", first["value"])
}
```

（`srSetup`/`srDoReq`/`srStr`/`srUID` 都是 `itsm-backend/handlers/service_request/handler_test.go` 里已经存在的 helper，签名见本文件前面"Task 4 Interfaces"一节引用的源码位置；`strconv` 该文件顶部已经 import 过。）

- [ ] **Step 4: 跑测试确认失败**

Run: `cd itsm-backend && go test ./handlers/service_request/... -run TestHandler_Get_IncludesCustomFieldValues -v`
Expected: FAIL（响应里没有 `customFields` key）

- [ ] **Step 5: `handler.go` 实现**

把 `toDTO` 拆成两个（保持原有那个不查字段值的版本给 `List` 用，新增一个查的版本给 `Get`/`Create` 详情响应用）：

```go
func (h *Handler) toDTOWithCustomFields(req *ServiceRequest, approvals []*ServiceRequestApproval, client *ent.Client) *dto.ServiceRequestResponse {
	resp := h.toDTO(req, approvals)
	if client == nil {
		return resp
	}
	values, err := service.NewFieldValueService(client).ListValues(context.Background(), req.TenantID, "service_request", req.ID)
	if err != nil {
		return resp
	}
	if len(values) == 0 {
		return resp
	}
	resp.CustomFields = make([]dto.CustomFieldValueResponse, 0, len(values))
	for _, v := range values {
		resp.CustomFields = append(resp.CustomFields, dto.CustomFieldValueResponse{Name: v.Name, Label: v.Label, Value: v.Value})
	}
	return resp
}
```

`Handler` struct 需要能拿到 `*ent.Client`：`Service` 上已经在 Task 3 加了 `client` 字段但未导出，给 `Service` 加一个导出方法：

```go
func (s *Service) Client() *ent.Client { return s.client }
```

改两处调用点（`ent`/`service` 两个包需要在 `handler.go` 顶部 import；如果已经因为其它原因 import 过，不要重复添加）：

`Get`（`handler.go:187`，原来是 `common.Success(c, h.toDTO(req, approvals))`）：

```go
	common.Success(c, h.toDTOWithCustomFields(req, approvals, h.service.Client()))
```

`Create`（`handler.go:163`，重新查一次详情成功之后那个分支，原来是 `common.Success(c, h.toDTO(fullReq, approvals))`）：

```go
	common.Success(c, h.toDTOWithCustomFields(fullReq, approvals, h.service.Client()))
```

`Create` 里 `h.service.Get` 失败时的兜底分支（`common.Success(c, h.toDTO(created, nil))`）保持不变——那条路径本身就是在记错误日志的降级返回，不需要额外查一次字段值。

`List`（190行起）调用 `h.toDTO(...)` 的地方保持不变，确认列表响应不带 `customFields`（避免逐条查询造成 N+1，跟工单列表接口的设计一致）。

- [ ] **Step 6: 跑测试确认通过**

Run: `cd itsm-backend && go test ./handlers/service_request/... -v 2>&1 | tail -80`
Expected: 全部 PASS，包括新加的这条

- [ ] **Step 7: Commit**

```bash
cd itsm-backend
gofmt -l .
git add dto/service_dto.go handlers/service_request/handler.go handlers/service_request/handler_test.go handlers/service_request/service.go
git commit -m "feat(backend): expose customFields on service request detail response"
```

---

### Task 5: 端到端集成测试

**Files:**
- Test: `itsm-backend/tests/integration/service_catalog_fields_test.go`（新建）

**Interfaces:**
- Consumes: 本 Task 只组装真实 Gin router + 真实 handler/service，不新增生产代码接口。参考脚手架：`itsm-backend/tests/integration/dynamic_fields_test.go`（工单那条链路的端到端测试，`setupDynamicFieldsRouter`/`doDynamicFieldsRequest` 的搭法）。

- [ ] **Step 1: 看一眼现有集成测试脚手架**

Run: `sed -n '1,60p' itsm-backend/tests/integration/dynamic_fields_test.go`

跟随同样的模式：`gin.New()` + 轻量 middleware 直接注入 `tenant_id`/`user_id`/`role`，挂载真实 `handlers/service_catalog.Handler` + `handlers/service_request.Handler`（不是完整 JWT/RBAC 中间件链）。

- [ ] **Step 2: 写集成测试**

```go
package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/handlers/cmdb"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/handlers/service_request"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupServiceCatalogFieldsRouter(t *testing.T) (*gin.Engine, *ent.Tenant, *ent.User) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:sc_fields_e2e?mode=memory&cache=shared&_fk=1")
	logger := zaptest.NewLogger(t).Sugar()

	ctx := t.Context()
	tenant, err := client.Tenant.Create().SetName("SC Fields Tenant").SetCode("SCFIELDS").SetDomain("scfields.test.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	user, err := client.User.Create().
		SetUsername("scfields_user").SetEmail("scfields@test.com").SetPasswordHash("hash").
		SetName("SC Fields User").SetDepartment("IT").SetPhone("1234567890").
		SetActive(true).SetRole("admin").SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, logger)
	scHandler := service_catalog.NewHandler(scService)

	srRepo := service_request.NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	srService := service_request.NewService(srRepo, scRepo, cmdbRepo, client, logger)
	srHandler := service_request.NewHandler(srService)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("tenant_id", tenant.ID)
		c.Set("user_id", user.ID)
		c.Set("role", "admin")
		c.Next()
	})
	r.POST("/api/v1/service-catalogs", scHandler.Create)
	r.GET("/api/v1/service-catalogs/:id", scHandler.Get)
	r.POST("/api/v1/service-requests", srHandler.Create)
	r.GET("/api/v1/service-requests/:id", srHandler.Get)

	return r, tenant, user
}

func doServiceCatalogFieldsRequest(t *testing.T, r http.Handler, method, path string, body interface{}) (apiEnvelope, int) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env apiEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env), "response body=%s", w.Body.String())
	return env, w.Code
}

// TestServiceCatalogFields 跑通"建服务目录（带1个自定义字段）-> 员工提交服务请求（填该字段）
// -> 请求详情 customFields 展示正确 -> 列表不带 customFields"的完整链路。
func TestServiceCatalogFields(t *testing.T) {
	r, _, _ := setupServiceCatalogFieldsRouter(t)

	createCatalogReq := map[string]interface{}{
		"name": "云主机申请", "category": "云服务", "description": "测试",
		"fields": []map[string]interface{}{
			{"name": "environment", "label": "环境", "type": "text", "required": true},
		},
	}
	env, status := doServiceCatalogFieldsRequest(t, r, http.MethodPost, "/api/v1/service-catalogs", createCatalogReq)
	require.Equal(t, http.StatusOK, status, "message=%s", env.Message)
	require.Equal(t, 0, env.Code)
	var catalogResp struct {
		ID     int                      `json:"id"`
		Fields []map[string]interface{} `json:"fields"`
	}
	require.NoError(t, json.Unmarshal(env.Data, &catalogResp))
	require.Len(t, catalogResp.Fields, 1)
	catalogID := catalogResp.ID

	createRequestReq := map[string]interface{}{
		"catalogId": catalogID, "title": "申请一台云主机", "reason": "测试",
		"complianceAck": true,
		"formData": map[string]interface{}{
			"environment": "production",
		},
	}
	env, status = doServiceCatalogFieldsRequest(t, r, http.MethodPost, "/api/v1/service-requests", createRequestReq)
	require.Equal(t, http.StatusOK, status, "message=%s", env.Message)
	require.Equal(t, 0, env.Code)
	var createdRequest struct {
		ID int `json:"id"`
	}
	require.NoError(t, json.Unmarshal(env.Data, &createdRequest))

	env, status = doServiceCatalogFieldsRequest(t, r, http.MethodGet, "/api/v1/service-requests/"+strconv.Itoa(createdRequest.ID), nil)
	require.Equal(t, http.StatusOK, status)
	var detail struct {
		CustomFields []struct {
			Name  string      `json:"name"`
			Label string      `json:"label"`
			Value interface{} `json:"value"`
		} `json:"customFields"`
	}
	require.NoError(t, json.Unmarshal(env.Data, &detail))
	require.Len(t, detail.CustomFields, 1)
	assert.Equal(t, "environment", detail.CustomFields[0].Name)
	assert.Equal(t, "环境", detail.CustomFields[0].Label)
	assert.Equal(t, "production", detail.CustomFields[0].Value)
}
```

（`apiEnvelope` 类型已经在 `itsm-backend/tests/integration/dynamic_fields_test.go` 里定义过，同一个 `integration` 包内直接复用，不要重新定义。）

- [ ] **Step 3: 跑集成测试**

Run: `cd itsm-backend && go test ./tests/integration/... -run TestServiceCatalogFields -v`
Expected: PASS

- [ ] **Step 4: 跑全量后端测试确认没有破坏其它东西**

Run: `cd itsm-backend && go test ./... 2>&1 | tail -60`
Expected: 全部 `ok`，无新增 `FAIL`（`TestUserController_CreateUser` 等几个跟本次改动无关的既有失败——已确认是环境里预置的密码策略问题，参考本仓库更早的调查记录——可以忽略，不是本次改动引入的）

- [ ] **Step 5: Commit**

```bash
cd itsm-backend
gofmt -l .
git add tests/integration/service_catalog_fields_test.go
git commit -m "test(backend): add end-to-end integration test for service catalog custom fields"
```

---

### Task 6: 前端——抽取共享的 `CustomFieldsEditor` 组件

**Files:**
- Create: `itsm-frontend/src/components/common/CustomFieldsEditor.tsx`
- Modify: `itsm-frontend/src/app/(main)/tickets/templates/page.tsx`
- Test: 手动验证（Ant Design `Form.List` UI 组件，用 `tsc --noEmit` 兜底类型正确性，不新增自动化测试——跟工单模板那次一致，模板管理这块目前是手动验证覆盖）

**Interfaces:**
- Produces：`CustomFieldsEditor`——一个不持有自己 `Form` 实例的纯展示型组件，`Form.List` 必须挂在父级 `Form` 内部才能读取表单上下文，所以它导出的是一段 `Form.List` 的 JSX，不是独立组件包一层 `<Form>`。

```tsx
export interface CustomFieldsEditorProps {
  /** Form.List 的字段名，比如 "customFields" */
  name: string;
}
export function CustomFieldsEditor(props: CustomFieldsEditorProps): JSX.Element;
```

- [ ] **Step 1: 建组件文件，把 `tickets/templates/page.tsx` 里已有的 `Form.List` 代码原样搬过去**

`itsm-backend/handlers/service_catalog` 那次工单模板实施已经在 `itsm-frontend/src/app/(main)/tickets/templates/page.tsx` 里写过一版这个编辑器（`<Divider>Custom Fields</Divider>` 到 `</Form.List>` 那一整段，包含字段名/标签/类型/选项/必填五列 + 增删按钮）。原样剪切这段 JSX，粘到新文件里，只把 `Form.List name="customFields"` 的 `name` 换成从 `props.name` 读：

```tsx
// itsm-frontend/src/components/common/CustomFieldsEditor.tsx
'use client';

import React from 'react';
import { Row, Col, Form, Input, Select, Switch, Button, Typography } from 'antd';
import { Plus, Delete } from 'lucide-react';

const { Text } = Typography;

export interface CustomFieldsEditorProps {
  /** Form.List 挂载用的字段名，比如父级表单里的 "customFields" */
  name: string;
}

export function CustomFieldsEditor({ name }: CustomFieldsEditorProps) {
  return (
    <>
      <Text type="secondary" style={{ display: 'block', marginBottom: 12 }}>
        提交时会额外展示这些字段；除字段名/标签/类型/是否必填外，其它元数据（placeholder、默认值等）目前后端不持久化。
      </Text>
      <Form.List name={name}>
        {(fields, { add, remove }) => (
          <>
            {fields.map(({ key, name: fieldName, ...restField }) => (
              <Row gutter={8} key={key} align="middle" style={{ marginBottom: 8 }}>
                <Col span={6}>
                  <Form.Item
                    {...restField}
                    name={[fieldName, 'name']}
                    rules={[{ required: true, message: 'Field name required' }]}
                    style={{ marginBottom: 0 }}
                  >
                    <Input placeholder="字段名，如 environment" />
                  </Form.Item>
                </Col>
                <Col span={6}>
                  <Form.Item
                    {...restField}
                    name={[fieldName, 'label']}
                    rules={[{ required: true, message: 'Label required' }]}
                    style={{ marginBottom: 0 }}
                  >
                    <Input placeholder="展示标签，如 环境" />
                  </Form.Item>
                </Col>
                <Col span={5}>
                  <Form.Item {...restField} name={[fieldName, 'type']} initialValue="text" style={{ marginBottom: 0 }}>
                    <Select
                      options={[
                        { value: 'text', label: 'Text' },
                        { value: 'textarea', label: 'Textarea' },
                        { value: 'number', label: 'Number' },
                        { value: 'date', label: 'Date' },
                        { value: 'select', label: 'Select' },
                      ]}
                    />
                  </Form.Item>
                </Col>
                <Col span={4}>
                  <Form.Item {...restField} name={[fieldName, 'options']} style={{ marginBottom: 0 }}>
                    <Input placeholder="选项(逗号分隔，仅Select)" />
                  </Form.Item>
                </Col>
                <Col span={2}>
                  <Form.Item
                    {...restField}
                    name={[fieldName, 'required']}
                    valuePropName="checked"
                    initialValue={false}
                    style={{ marginBottom: 0 }}
                  >
                    <Switch checkedChildren="必填" unCheckedChildren="选填" />
                  </Form.Item>
                </Col>
                <Col span={1}>
                  <Button type="text" danger icon={<Delete size={16} />} onClick={() => remove(fieldName)} aria-label="删除字段" />
                </Col>
              </Row>
            ))}
            <Form.Item style={{ marginBottom: 0 }}>
              <Button type="dashed" onClick={() => add({ type: 'text', required: false })} icon={<Plus size={16} />} block>
                添加自定义字段
              </Button>
            </Form.Item>
          </>
        )}
      </Form.List>
    </>
  );
}
```

- [ ] **Step 2: `tickets/templates/page.tsx` 改成调用这个组件**

把原来那一大段 `<Form.List name="customFields">...</Form.List>` JSX（含前面那句 `<Divider>Custom Fields</Divider>` 和提示文字）替换成：

```tsx
<Divider>Custom Fields</Divider>
<CustomFieldsEditor name="customFields" />
```

文件顶部 `import` 加：

```tsx
import { CustomFieldsEditor } from '@/components/common/CustomFieldsEditor';
```

删掉原来那段 JSX 用到的、现在不再需要的 import（如果 `Switch` 等组件在这个文件别处已经没有其它用途了，检查一下再决定要不要删——不要为了删而删，先确认没有其它 `<Switch>` 用法残留）。

- [ ] **Step 3: 类型检查**

Run: `cd itsm-frontend && npx tsc --noEmit 2>&1 | tail -40`
Expected: 无输出

- [ ] **Step 4: 手动验证**

启动本地环境（参考 `reference-native-local-dev-setup` 记忆里记录的启动命令），登录后打开 `/tickets/templates`，编辑一个模板，确认自定义字段编辑器渲染跟之前一致（这一步纯粹是"抽取组件没改变行为"的回归验证，不是新功能）。

- [ ] **Step 5: Commit**

```bash
cd itsm-frontend
git add src/components/common/CustomFieldsEditor.tsx "src/app/(main)/tickets/templates/page.tsx"
git commit -m "refactor(frontend): extract CustomFieldsEditor from ticket templates page for reuse in service catalog"
```

---

### Task 7: 前端——`/admin/service-catalogs` 接入 `CustomFieldsEditor`

**Files:**
- Modify: `itsm-frontend/src/types/service-catalog.ts`
- Modify: `itsm-frontend/src/lib/api/service-catalog-api.ts`
- Modify: `itsm-frontend/src/app/(main)/admin/service-catalogs/page.tsx`

**Interfaces:**
- Consumes: `CustomFieldsEditor`（Task 6 产出）
- Produces：`ServiceItem`/`CreateServiceItemRequest`/`UpdateServiceItemRequest` 新增 `fields?: Array<{name:string; label:string; type:string; required:boolean; options?: Array<{label:string; value:string}>}>`

- [ ] **Step 1: `types/service-catalog.ts` 加类型**

在 `ServiceItem` interface（42行）里加：

```ts
  // 自定义字段定义（field_definitions），提交请求时前端按这里渲染动态输入项
  fields?: Array<{
    name: string;
    label: string;
    type: string;
    required: boolean;
    options?: Array<{ label: string; value: string }>;
  }>;
```

`CreateServiceItemRequest`（393行）里加同样的 `fields?:` 字段（类型复用 `ServiceItem['fields']`）：

```ts
  fields?: ServiceItem['fields'];
```

（`UpdateServiceItemRequest` 是 `Partial<CreateServiceItemRequest>`，自动带上，不用单独改。）

- [ ] **Step 2: `service-catalog-api.ts` 的 `toServiceItem` 透传 `fields`**

在 `toServiceItem`（66行）返回对象里加：

```ts
      fields: Array.isArray(raw?.fields) ? raw.fields : [],
```

`createService`（194行）的 `payload` 对象里加：

```ts
      fields: request.fields,
```

`updateService` 同理（找到它构建 `payload` 的地方，同样加 `if (request.fields !== undefined) payload.fields = request.fields;`）。

- [ ] **Step 3: 类型检查确认 Step 1/2 没引入类型错误**

Run: `cd itsm-frontend && npx tsc --noEmit 2>&1 | tail -40`
Expected: 无输出

- [ ] **Step 4: `admin/service-catalogs/page.tsx` 接入编辑器**

在 `deliveryTime`/`serviceType` 那个 `<Row gutter={16}>`（约738行）之后插入：

```tsx
<Divider>自定义字段</Divider>
<CustomFieldsEditor name="fields" />
```

文件顶部加 `import { CustomFieldsEditor } from '@/components/common/CustomFieldsEditor';`（确认 `Divider` 已经从 `antd` 导入——这个文件已经有大量 `<div className="bg-gray-50 p-4 rounded-lg mb-4">` 分组区块，若没有 `Divider` 导入就用同样风格的 `<div>` 分组代替，不强求视觉上跟工单模板页完全一致，保持跟这个文件已有的分组视觉风格统一）。

`handleSubmit`（132行）的 `payload` 对象里加：

```ts
        fields: values.fields,
```

`handleEdit`（168行）的 `form.setFieldsValue({...})` 里加：

```ts
      fields: catalog.fields || [],
```

- [ ] **Step 5: 类型检查**

Run: `cd itsm-frontend && npx tsc --noEmit 2>&1 | tail -40`
Expected: 无输出

- [ ] **Step 6: 手动验证**

`/admin/service-catalogs` 新建一个服务，加一个自定义字段（比如 `environment`/环境），保存后重新打开编辑，确认字段还在（不是空的——这一步专门验证 Task 6 抽取组件、Task 7 接入表单联动没有重现之前工单模板那次因为 `Modal` 缺 `destroyOnHidden` 导致表单状态跟显示对不上的问题；`admin/service-catalogs` 这个 Modal 本身已经用 `form.resetFields()`/`form.setFieldsValue()` 手动管理状态、不依赖 `initialValues` 只生效一次那个默认行为，所以理论上不会重现同一个 bug，但仍需实测确认）。

- [ ] **Step 7: Commit**

```bash
cd itsm-frontend
git add src/types/service-catalog.ts src/lib/api/service-catalog-api.ts "src/app/(main)/admin/service-catalogs/page.tsx"
git commit -m "feat(frontend): wire CustomFieldsEditor into service catalog admin page"
```

---

### Task 8: 前端——员工侧 `/service-catalog/request/[id]` 动态渲染 + 提交自定义字段

**Files:**
- Modify: `itsm-frontend/src/app/(main)/service-catalog/request/[id]/page.tsx`

**Interfaces:**
- Consumes: `GET /api/v1/service-catalogs/:id` 响应里的 `fields`（该页面已有的 `catalog` state 直接就是这个响应体，见现有代码59-61行 `setCatalog(data?.data || data)`）

- [ ] **Step 1: 在表单里加动态字段渲染区块**

在现有的 `<Form ...>` 内、`complianceAck`/`expireAt` 那组 `Form.Item` 之后（提交按钮之前）插入：

```tsx
{Array.isArray(catalog?.fields) && catalog.fields.length > 0 && (
  <>
    <Divider>该服务的补充信息</Divider>
    {catalog.fields.map((field: { name: string; label: string; type: string; required: boolean; options?: Array<{ label: string; value: string }> }) => (
      <Form.Item
        key={field.name}
        name={['customFields', field.name]}
        label={field.label}
        rules={field.required ? [{ required: true, message: `请填写${field.label}` }] : []}
      >
        {field.type === 'textarea' ? (
          <TextArea rows={3} />
        ) : field.type === 'select' ? (
          <Select options={field.options} />
        ) : field.type === 'number' ? (
          <Input type="number" />
        ) : field.type === 'date' ? (
          <DatePicker style={{ width: '100%' }} />
        ) : (
          <Input />
        )}
      </Form.Item>
    ))}
  </>
)}
```

（`Divider` 需要加进顶部 `antd` 的 import 列表；`DatePicker` 已经导入过。）

- [ ] **Step 2: `onFinish` 提交时把动态字段值合并进 `formData`**

在 `onFinish`（85行）构造 `payload.formData` 的对象字面量里，展开 `values.customFields`（Ant Design `Form.Item name={['customFields', field.name]}` 会让 `values.customFields` 变成一个 `{[fieldName]: value}` 的对象，直接展开进 `formData` 即可，跟后端 `handlers/service_request` 的 `extractServiceRequestFieldValues` 读取 `FormData` 顶层键的方式对齐）：

```tsx
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
          complianceAck: !!values.complianceAck,
          expireAt: expireAt ? expireAt.toISOString() : undefined,
          ...(values.customFields || {}),
        },
```

- [ ] **Step 3: 类型检查**

Run: `cd itsm-frontend && npx tsc --noEmit 2>&1 | tail -40`
Expected: 无输出

- [ ] **Step 4: 手动验证**

用 Task 7 里加了 `environment` 字段的服务，打开它的申请页 `/service-catalog/request/:id`，确认表单上出现"环境"输入框，填完提交，去 `/my-requests` 或详情页确认能看到这次申请。

- [ ] **Step 5: Commit**

```bash
cd itsm-frontend
git add "src/app/(main)/service-catalog/request/[id]/page.tsx"
git commit -m "feat(frontend): render and submit service catalog custom fields on request page"
```

---

### Task 9: 前端——服务请求详情页展示 `customFields`

**Files:**
- Modify: `itsm-frontend/src/components/service-request/ServiceRequestDetail.tsx`

**Interfaces:**
- Consumes: `request.customFields`（`ServiceCatalogApi.toServiceRequest` 用 `{...raw, ...}` 展开，`raw.customFields` 已经自动透传，不需要改 `service-catalog-api.ts`）

- [ ] **Step 1: 加展示区块**

在现有的 `<Descriptions column={1} bordered>`（193行）里、`{request.formData && (...)}` 那个 `Descriptions.Item`（203-209行）之前插入：

```tsx
{Array.isArray(request.customFields) && request.customFields.length > 0 && (
  <>
    {request.customFields.map((field: { name: string; label: string; value: unknown }) => (
      <Descriptions.Item key={field.name} label={field.label}>
        {String(field.value)}
      </Descriptions.Item>
    ))}
  </>
)}
```

保留原有的"表单数据"整段 JSON 展示（`request.formData` 那个 `Descriptions.Item`）不动——那是给系统已知字段和调试用的兜底展示，`customFields` 是新加的、给动态字段用的结构化展示，两者并存不冲突。

- [ ] **Step 2: 类型检查**

Run: `cd itsm-frontend && npx tsc --noEmit 2>&1 | tail -40`
Expected: 无输出

- [ ] **Step 3: 手动验证**

打开 Task 8 里提交的那条服务请求详情页，确认"环境"这一行单独展示、值是"production"（或你实际填的值），不是挤在下面那坨 JSON 里。

- [ ] **Step 4: Commit**

```bash
cd itsm-frontend
git add src/components/service-request/ServiceRequestDetail.tsx
git commit -m "feat(frontend): display service request custom field values on detail page"
```

---

### Task 10: 废弃 `ServiceCatalogItem`——删除孤立 legacy 代码 + 补迁移文件

**范围修正（执行时发现，非原计划）**：Task 10 首次派发时，implementer 重新跑 Step 1 的验证 grep 发现结论有误——`service.ServiceCatalogService`/`ServiceRequestService` 并不是零路由的孤立代码，而是通过 `controller/service_controller.go`（`ServiceController`，479 行，13 个方法）真实挂在 `router.go:1257-1274` 的 `/api/v1/services/catalogs*`、`/api/v1/services/requests*` 这组路由上（`internal/bootstrap/app.go:262,381,410,729` 完成依赖注入）。这是原设计阶段调查遗漏的第三套实现。经过人工确认：前端全文搜索、`itsm-cli`、`itsm-agent` 均无任何调用这组 `/services/catalogs`、`/services/requests` 接口的地方，`controller/service_controller.go` 也没有任何测试引用（`grep -rln "ServiceController\b" --include=*_test.go .` 零命中）——即"路由是注册的，但没有真实消费方"。已经跟人类确认：本次连这套 `ServiceController` + 路由一起清理，不只是原计划里的 `ServiceCatalogItem`/两个 legacy service 文件。

**Files:**
- Delete: `itsm-backend/controller/service_controller.go`
- Delete: `itsm-backend/service/service_catalog_item_service.go`
- Delete: `itsm-backend/service/service_catalog_item_service_test.go`
- Delete: `itsm-backend/service/service_catalog_service.go`
- Delete: `itsm-backend/service/service_catalog_service_test.go`（Step 1 验证时发现的额外文件，只测这个 legacy service，随其一起删）
- Delete: `itsm-backend/service/service_request_service.go`
- Delete: `itsm-backend/ent/schema/service_catalog_item.go`
- Modify: `itsm-backend/router/router.go`（删掉 248 行 `ServiceController *controller.ServiceController` 字段、1257-1274 行整个 `if config.ServiceController != nil {...}` 路由注册块）
- Modify: `itsm-backend/internal/bootstrap/app.go`（删掉 262/381/410/729 行：`serviceCatalogService`/`serviceRequestService`/`serviceController` 的构造和 `ServiceController: serviceController` 字段赋值——注意 381 行 `service.NewServiceRequestService(client, sugar, approvalService, notificationService)` 用到的 `approvalService`/`notificationService` 这两个变量很可能在文件其它地方还有别的用途，删除这一行前确认它们不会变成未使用变量导致编译报错；如果只有这一处用到，对应的构造也要一并清理，不要留下 unused variable）
- Modify: `itsm-backend/ent/schema/servicecatalog.go`（去掉 `edge.To("items", ServiceCatalogItem.Type)`）
- Create: `itsm-backend/migrations/xxxxxxxx_drop_service_catalog_item.sql`（文件名前缀按仓库既有迁移文件的编号规则决定，先看一眼 `ls itsm-backend/migrations/` 确认命名规则）

**Interfaces:** 无新增/变更的代码接口——本 Task 纯删除。删除后 `/api/v1/services/catalogs*`、`/api/v1/services/requests*` 这组路由不再存在（确认过没有真实调用方，属于安全删除）。

- [ ] **Step 1: 再次确认这几个文件真的没有其它路由/依赖注入引用（`ServiceController` 本身除外，它就是本次要删的）**

Run:
```bash
cd itsm-backend
grep -rn "ServiceCatalogItemService\|service.NewServiceCatalogService\b\|service.NewServiceRequestService\b|ServiceController\b" --include=*.go . | grep -v "_test.go\|service/service_catalog_item_service.go\|service/service_catalog_service.go\|service/service_request_service.go\|controller/service_controller.go\|router/router.go\|internal/bootstrap/app.go"
```
Expected: 无输出。如果有输出且不是本 Task 计划删除/修改的文件，停下来找到那个调用方、评估能不能一起清理，不要在有未知调用方的情况下继续删除。

- [ ] **Step 2: 先删 `ServiceController` 和它的路由/依赖注入，再删底层 service**

顺序很重要——先让 `ServiceController` 及其路由/构造彻底消失，底层的 `service.ServiceCatalogService`/`ServiceRequestService` 才会变成真正无引用的死代码，此时删除它们才不会破坏编译。

```bash
git rm itsm-backend/controller/service_controller.go
```

编辑 `itsm-backend/router/router.go`：删掉 248 行 `ServiceController *controller.ServiceController` 字段声明，删掉 1257-1274 行整个 `// Service Catalog & Service Requests` + `if config.ServiceController != nil { ... }` 块。

编辑 `itsm-backend/internal/bootstrap/app.go`：删掉 410 行 `serviceController := controller.NewServiceController(...)`、729 行 `ServiceController: serviceController,`；删掉 262 行 `serviceCatalogService := service.NewServiceCatalogService(client, sugar)`；检查 381 行 `serviceRequestService := service.NewServiceRequestService(client, sugar, approvalService, notificationService)`——如果 `serviceRequestService` 这个变量删除构造后在文件里再没有其它引用，把这行也删掉；如果 `approvalService`/`notificationService` 这两个入参变量除了这一行没有被其它代码使用，会变成 unused variable 编译错误，需要一并处理（多半这两个变量在文件别处也被其它构造函数用到，正常情况下不需要改，只是提醒检查）。

Run: `cd itsm-backend && go build ./controller/... ./router/... ./internal/bootstrap/... 2>&1 | tail -60`
Expected: 无输出（确认 `ServiceController` 清理干净，没有遗漏引用）

- [ ] **Step 3: 删除底层 legacy service 文件**

```bash
git rm itsm-backend/service/service_catalog_item_service.go
git rm itsm-backend/service/service_catalog_item_service_test.go
git rm itsm-backend/service/service_catalog_service.go
git rm itsm-backend/service/service_catalog_service_test.go
git rm itsm-backend/service/service_request_service.go
git rm itsm-backend/ent/schema/service_catalog_item.go
```

- [ ] **Step 4: `ent/schema/servicecatalog.go` 去掉 `items` edge**

Run: `grep -n "items" itsm-backend/ent/schema/servicecatalog.go`，删掉那一行 `edge.To("items", ServiceCatalogItem.Type)`（如果 `Edges()` 方法删空了只剩一个空的 `[]ent.Edge{}`，保留方法本身，不要连方法一起删——`Edges()` 是 `ent.Schema` 接口要求实现的方法）。

- [ ] **Step 5: 重新生成 Ent 代码**

Run: `cd itsm-backend && go generate ./ent 2>&1 | tail -40`
Expected: 生成成功，`ent/servicecatalogitem*.go`、`ent/schema/service_catalog_item.go` 等文件被自动清理（Ent 的代码生成器会根据 schema 文件删除对应的生成代码）

- [ ] **Step 6: 编译，看还有哪里引用了已删除的类型**

Run: `cd itsm-backend && go build ./... 2>&1 | tail -60`

如果报错提示某处还在用 `ent.ServiceCatalogItem`/`ServiceCatalogItemService` 之类的符号，去掉那个引用点（预期不会有，Step 1/2 已经确认过没有真实调用方）。

Expected: 最终无输出（编译通过）

- [ ] **Step 7: 补迁移文件**

Run: `ls itsm-backend/migrations/ | tail -10` 看命名规则（时间戳前缀还是序号前缀），照着建一个新文件：

```sql
-- itsm-backend/migrations/<按现有规则命名>_drop_service_catalog_item.sql
DROP TABLE IF EXISTS service_catalog_items;
ALTER TABLE service_catalogs DROP COLUMN IF EXISTS form_schema;
```

（`ServiceCatalog.form_schema` 本身在业务代码里从未被写入过——设计文档已经记录这个判断依据——这次跟着 `service_catalog_items` 表一起清理干净，不留孤立列。）

在 `itsm-backend/migration/migrator.go` 里确认新迁移文件会被自动发现执行（这个项目的迁移器通常是扫描 `migrations/` 目录按文件名排序执行，具体确认方式：`grep -n "ReadDir\|Glob\|migrations" itsm-backend/migration/migrator.go`），不需要手动注册文件名的话这一步就是纯确认，不用改代码。

- [ ] **Step 8: 跑全量测试确认没有隐藏依赖**

Run: `cd itsm-backend && gofmt -l . && go test ./... 2>&1 | tail -80`
Expected: `gofmt -l .` 无输出；`go test` 全部 `ok`（`service/service_catalog_item_service_test.go`、`service/service_catalog_service_test.go` 已经随源文件一起删除，不会再跑那几条测试；除了本仓库已知的、跟本次改动无关的密码策略/incident 状态流转既有失败外不应该有新增 `FAIL`）

- [ ] **Step 9: Commit**

```bash
cd itsm-backend
git add -A
git commit -m "refactor(backend): remove orphaned ServiceCatalogItem + ServiceController branches (zero real callers, superseded by handlers/service_catalog custom fields)

- delete service/service_catalog_item_service.go (+test) and service/service_catalog_service.go (+test), service/service_request_service.go
- delete controller/service_controller.go (479 lines, 13 handlers, zero real callers)
- remove /api/v1/services/catalogs* and /api/v1/services/requests* route registrations from router/router.go
- remove dead wiring from internal/bootstrap/app.go
- drop ent ServiceCatalogItem schema + items edge on ServiceCatalog
- add migration to drop service_catalog_items table and servicecatalogs.form_schema column"
```

---

### Task 11: 最终验证

**Files:** 无改动，纯验证。

- [ ] **Step 1: 后端全量测试 + 格式检查**

Run: `cd itsm-backend && gofmt -l . && go build ./... && go test ./... 2>&1 | tail -100`
Expected: `gofmt -l .` 无输出；build 无输出；测试全部 `ok`

- [ ] **Step 2: 前端类型检查 + 全量测试**

Run: `cd itsm-frontend && npx tsc --noEmit && npx jest 2>&1 | tail -60`
Expected: 无类型错误；Jest 测试全部通过（不应该有因为本次改动导致的新增失败）

- [ ] **Step 3: 手动跑一遍完整链路**

按照设计文档"测试计划"一节列的手动验证路径，在本地环境里走一遍：管理员在 `/admin/service-catalogs` 新建服务并加字段 → 员工在 `/service-catalog` 找到该服务申请 → 提交时看到并填写自定义字段 → `/service-requests/:id` 详情页正确展示。

- [ ] **Step 4: Commit（如果 Step 1-3 发现并修复了问题）**

如果前几步全部一次通过，这一步不需要新 commit。如果发现问题并修复，正常提交，commit message 具体描述修的是什么（不要用"fix issues"这种空泛描述）。

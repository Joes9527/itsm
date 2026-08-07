# 服务请求（ServiceRequest）委托给 Ticket Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `ServiceRequest` 不再维护自己的状态机/三级审批/工作流触发，改为在创建时同步创建一条关联 `Ticket`，状态/审批/BPMN 工作流全部委托给该 Ticket；`ServiceRequest` 瘦身为只保留服务目录来源特有的字段（cost center、合规确认、资源交付等）。

**Architecture:** `ServiceRequest.Create()` 先调用 `TicketService.CreateTicket`（`source="service_catalog"`）触发既有的 BPMN 工作流，再创建携带 `ticket_id` 外键的瘦身版 `ServiceRequest` 行。`ServiceRequestApproval` 表整体删除，审批记录读写全部走 Ticket 侧已有的 `process_approval_decision`（BPMN）。前端退休两个独立的 SR 详情页，统一到 `/tickets/:ticketId` 详情页 + 一个"服务申请信息"面板。

**Tech Stack:** Go + Gin + Ent ORM + PostgreSQL；Next.js + TypeScript + Ant Design 6。

参考设计文档：`docs/superpowers/specs/2026-08-07-service-request-ticket-unification-design.md`

## Global Constraints

- 后端 DTO 响应字段一律 camelCase，Ent Schema 字段一律 snake_case，Mapper 负责转换（CLAUDE.md 硬性规则）。
- 新查询必须带 `tenantID` 过滤（租户隔离硬性规则）。
- **Task 1 内部的 ent schema 改动 + 迁移文件 + `handlers/service_request/*` 全量重写 + `service/provisioning_service.go` 改动必须在同一个 commit 落地，不能拆开分批提交**——schema 删字段后，旧代码在没重写之前编译不过。这是设计文档明确写的硬性约束（见设计文档"实施顺序"一节）。
- **执行前提**：本任务必须在包含 `feat/dynamic-custom-fields` 分支（`origin/main` commit `77c3cc12` 或更新）的代码基础上执行——也就是说，执行本计划时创建的 worktree/分支必须从 `origin/main` fork，不能从本地分叉的 `main` fork（本地 main 缺少 Task 10 对 `controller/service_controller.go`/`service/service_request_service.go` 的删除，会导致本计划里改的 `handlers/service_request` 之外还存在另一套更旧的、本计划不处理的 SR 实现，产生混淆）。
- **相对设计文档的一处实现修正**：设计文档写的是"（同一事务）先创建 Ticket 再创建 ServiceRequest"。实际调查 `TicketService.CreateTicket` 后发现它是一个自包含的高层编排方法（内部做校验、模板处理、BPMN 触发等），不暴露外部事务句柄，无法干净地包进调用方的一个 DB 事务里。改为**顺序创建、非同一事务**：先调 `TicketService.CreateTicket`（它自己成功提交），再创建 `ServiceRequest` 行；若第二步失败，Ticket 已经存在但没有关联的 SR 扩展行——这与本仓库已有的"创建后写卫星数据、失败只记警告不回滚主记录"先例（`CreateTicket` 写 `field_values` 的模式）一致，不是新发明的容错策略。前端渲染服务申请面板时必须处理"ticket 存在但查不到关联 SR"的情况（不崩溃，不显示面板即可）。
- **审批端点处理方式**：设计文档里"转发到 ticket 域审批接口"这一步经确认不需要——`TicketDetail.tsx` 已经有现成的审批 UI（`ApprovalWorkflowPanel` + 批准按钮），SR 详情页退休、统一到 ticket 详情页后自动获得这个能力，不需要新写转发代码。`handlers/service_request` 里的 `ApplyApproval`/`ListPendingApprovals` 相关 handler/route/service 方法直接删除，不做转发层。

---

### Task 1：后端——ServiceRequest 瘦身 + 委托给 Ticket（原子提交）

**Files:**
- Modify: `itsm-backend/ent/schema/ticket.go`（加 `source` 字段）
- Modify: `itsm-backend/ent/schema/servicerequest.go`（瘦身，加 `ticket_id`）
- Delete: `itsm-backend/ent/schema/servicerequestapproval.go`
- Create: `itsm-backend/migrations/<按现有命名规则>_service_request_delegates_to_ticket.sql`
- Modify: `itsm-backend/migration/migrations.go`（注册上面的迁移）
- Modify: `itsm-backend/handlers/service_request/entity.go`
- Modify: `itsm-backend/handlers/service_request/service.go`
- Modify: `itsm-backend/handlers/service_request/handler.go`
- Modify: `itsm-backend/handlers/service_request/repository_impl.go`
- Modify: `itsm-backend/dto/service_dto.go`（`ServiceRequestResponse`/`CreateServiceRequestRequest`/`UpdateServiceRequestRequest` 瘦身，删除 `ServiceRequestApprovalResponse`/`ServiceRequestApprovalActionRequest`）
- Modify: `itsm-backend/service/provisioning_service.go`（`CreateTaskFromServiceRequest` 的前置检查与状态回写）
- Modify: `itsm-backend/router/router.go`（删除 `/service-requests/:id/approvals` GET/POST、`/service-requests/approvals/pending`、`/service-requests/:id/status` 路由；已有 `/tickets/approval/submit` 等路由不变）
- Modify: `itsm-backend/handlers/service_request/handler_test.go`、`handlers/service_request/service_test.go`、`handlers/service_request/handler_bpmn_bridge_test.go`（同步改写测试）
- Test: `itsm-backend/handlers/service_request/service_test.go`（新增委托相关测试）

**Interfaces:**
- Consumes: `TicketService.CreateTicket(ctx, req *dto.CreateTicketRequest, tenantID int) (*ticket.Ticket, error)`（已存在，`itsm-backend/service/ticket_service.go:125`）；`service.NewFieldValueService(client).CreateValues(...)`（已存在，签名不变，仅调用方传的 `entityType`/`entityID` 从 `"service_request"`/`SR.ID` 改为 `"ticket"`/`ticket.ID`）
- Produces：`ServiceRequest`（domain struct）新增 `TicketID int` 字段，供 Task 2/3 前端联查使用；`dto.ServiceRequestResponse` 新增 `ticketId int` 字段（camelCase：`ticketId`）

- [ ] **Step 1: ent schema 改动**

`itsm-backend/ent/schema/ticket.go` 的 `Fields()` 里加一行（放在 `type` 字段附近即可，位置不影响生成）：

```go
		field.String("source").
			Comment("工单来源：manual=手动创建，service_catalog=服务目录申请").
			Default("manual").
			Optional(),
```

`itsm-backend/ent/schema/servicerequest.go` 的 `Fields()` 整体替换为：

```go
func (ServiceRequest) Fields() []ent.Field {
	return []ent.Field{
		// 基础信息
		field.Int("tenant_id").Comment("租户ID").Positive(),
		field.Int("ticket_id").Comment("关联的Ticket ID——状态/审批/工作流全部委托给它").Positive(),
		field.Int("catalog_id").Comment("服务目录ID").Positive(),
		field.Int("ci_id").Comment("关联CI ID").Optional(),
		field.Int("requester_id").Comment("申请人ID").Positive(),

		// 表单数据
		field.JSON("form_data", map[string]any{}).Comment("表单数据").Optional(),
		field.String("cost_center").Comment("成本中心").Optional(),
		field.String("data_classification").Comment("数据分级：public|internal|confidential").Default("internal"),
		field.Bool("needs_public_ip").Comment("是否需要公网访问").Default(false),
		field.JSON("source_ip_whitelist", []string{}).Comment("源IP白名单").Optional(),
		field.Time("expire_at").Comment("到期时间").Optional(),
		field.Bool("compliance_ack").Comment("合规条款确认").Default(false),

		// 实施信息（资源交付，不属于本次重构范围，原样保留）
		field.Int("processor_id").Comment("处理人ID").Optional(),
		field.Time("started_at").Comment("开始处理时间").Optional(),
		field.Time("completed_at").Comment("完成时间").Optional(),
		field.Text("completion_note").Comment("完成备注").Optional(),
		field.Text("last_error").Comment("最近一次错误信息").Optional(),
		field.Int("version").Comment("乐观锁版本").Default(1).Positive(),

		// 时间戳
		field.Time("created_at").Comment("创建时间").Default(time.Now),
		field.Time("updated_at").Comment("更新时间").Default(time.Now).UpdateDefault(time.Now),
		field.Time("deleted_at").Comment("软删除时间").Optional().Nillable(),
	}
}
```

删掉的字段：`status`、`title`、`reason`、`current_level`、`total_levels`、`current_approver`、`approved_at`、`approver_comment`、`approval_history`（全部委托给关联 Ticket）。

`Indexes()` 里把 `index.Fields("tenant_id", "status", "created_at")`、`index.Fields("tenant_id", "current_level")` 删掉（字段没了），加一条 `index.Fields("ticket_id").Unique()`（保证 1:1）：

```go
func (ServiceRequest) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("ticket_id").Unique(),
		index.Fields("tenant_id", "created_at"),
		index.Fields("tenant_id", "requester_id", "created_at"),
		index.Fields("tenant_id", "ci_id"),
	}
}
```

删除整个文件 `itsm-backend/ent/schema/servicerequestapproval.go`。

- [ ] **Step 2: `go generate ./ent`**

Run: `cd itsm-backend && go generate ./ent 2>&1 | tail -60`
Expected: 生成成功；`ent/servicerequestapproval*.go` 系列文件被清理；`ent/servicerequest.go`/`ent/servicerequest/*.go` 反映新字段。

（此时 `go build ./...` 会大面积报错——`handlers/service_request/*.go` 还在引用被删字段/被删类型，这是预期状态，继续往下做，不要在这里停下验证编译。）

- [ ] **Step 3: `handlers/service_request/entity.go` 瘦身**

整个文件替换为：

```go
package service_request

import (
	"context"
	"time"
)

// ServiceRequest represents the domain entity for a service request.
// 状态/标题/审批全部委托给关联的 Ticket（TicketID）——本实体只保留服务目录来源
// 特有的字段（成本中心/合规/资源交付等）。
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
	Version            int
	ProcessorID        *int
	StartedAt          *time.Time
	CompletedAt        *time.Time
	CompletionNote     string
	LastError          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ListFilters defines filters for listing service requests
type ListFilters struct {
	UserID int // Requester ID
	Page   int
	Size   int
}

// Repository defines the interface for data persistence
type Repository interface {
	Create(ctx context.Context, req *ServiceRequest) (*ServiceRequest, error)
	Get(ctx context.Context, id, tenantID int) (*ServiceRequest, error)
	GetByTicketID(ctx context.Context, ticketID, tenantID int) (*ServiceRequest, error)
	List(ctx context.Context, tenantID int, filters ListFilters) ([]*ServiceRequest, int, error)
	Update(ctx context.Context, req *ServiceRequest) error
	Delete(ctx context.Context, req *ServiceRequest) error
	GetUserContext(ctx context.Context, userID, tenantID int) (department, name string, err error)
}
```

去掉了：`Status`/`Title`/`Reason`/`CurrentLevel`/`TotalLevels`/`ApprovedAt` 字段；`ServiceRequestApproval` 结构体整个删除；`ListFilters.Status` 删除（列表按 ticket 状态过滤留到 Task 2/3 前端调整，本任务不做，见 Step 5 的 `List` 说明）；`Repository` 接口删除 `UpdateStatus`/`GetApproval`/`UpdateApproval`/`UpdateRequestAndApproval`/`ListPendingApprovals`，新增 `GetByTicketID`（Task 2 前端渲染 ticket 详情页的 SR 面板要用）。

- [ ] **Step 4: `handlers/service_request/service.go` 重写**

删除的常量：`ApprovalStepManager`/`ApprovalStepIT`/`ApprovalStepSecurity`、`SRStatus*` 全部、`ApprovalStatus*` 全部、`ApprovalTimeout*` 全部（角色常量 `RoleAdmin`/`RoleSuperAdmin`/`RoleManager`/`RoleAgent`/`RoleTechnician`/`RoleSecurity` 保留，`Update`/`Delete` 权限判断还要用）。

`Service` 结构体删除 `approvalBridge *service.BPMNApprovalBridge` 字段；`NewService` 构造函数删除桥接初始化那段。签名不变：`NewService(repo Repository, scRepo service_catalog.Repository, cmdbRepo cmdb.Repository, entClient *ent.Client, logger *zap.SugaredLogger) *Service`（少了内部字段，参数不变）。

`Create` 方法改为（`import` 需要加 `itsm-backend/dto`已有、`itsm-backend/service` 已有，新增无）：

```go
// Create submits a new service request. 先创建关联 Ticket（承担状态/审批/工作流），
// 再创建瘦身后的 ServiceRequest 行——两步顺序执行，不在同一数据库事务里（TicketService.CreateTicket
// 是自包含的高层编排方法，不暴露外部事务句柄）。若第二步失败，Ticket 已经存在但缺少关联的
// SR 扩展数据——这与 CreateTicket 自己写 field_values 的失败处理是同一种"创建后写卫星数据，
// 失败只警告不回滚主记录"模式，不是新发明的容错策略。
func (s *Service) Create(ctx context.Context, tenantID, requesterID int, catalogID int, reqData *ServiceRequest) (*ServiceRequest, error) {
	if _, _, err := s.repo.GetUserContext(ctx, requesterID, tenantID); err != nil {
		return nil, common.NewBadRequestError("Requester not found or inactive", err)
	}
	// 1. Validate Service Catalog
	cat, err := s.scRepo.Get(ctx, tenantID, catalogID)
	if err != nil {
		return nil, common.NewNotFoundError("Service Catalog not found")
	}
	if cat.CloudServiceID > 0 && cat.CITypeID == 0 {
		return nil, common.NewBadRequestError("关联云服务时必须配置CI类型", nil)
	}
	if cat.Status != "enabled" && cat.Status != "active" {
		return nil, common.NewBadRequestError("Service Catalog is not enabled", nil)
	}

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

	// 2b. Validate required dynamic custom fields (server-side enforcement).
	if s.client != nil {
		fieldValues := extractServiceRequestFieldValues(reqData.FormData)
		defs, err := service.NewFieldDefinitionService(s.client).ListDefinitions(ctx, tenantID, "service_catalog", catalogID)
		if err != nil {
			return nil, common.NewInternalError("Failed to load service catalog fields", err)
		}
		for _, def := range defs {
			if !def.Required {
				continue
			}
			val, ok := fieldValues[def.Name]
			if !ok || val == nil || val == "" {
				return nil, common.NewBadRequestError(fmt.Sprintf("字段「%s」为必填项", def.Label), nil)
			}
		}
	}

	// 3. 先创建关联 Ticket——source="service_catalog" 标记来源，
	// description 用申请原因（reqData.reason() 从 FormData 兜底取，逻辑同下方 title()）。
	ticketReq := &dto.CreateTicketRequest{
		Title:       title,
		Description: reqData.reason(),
		Priority:    "medium",
		Type:        "service_request",
		RequesterID: requesterID,
	}
	createdTicket, err := s.ticketSvc.CreateTicket(ctx, ticketReq, tenantID)
	if err != nil {
		return nil, common.NewInternalError("Failed to create linked ticket", err)
	}

	newReq := &ServiceRequest{
		TenantID:           tenantID,
		TicketID:           createdTicket.ID,
		CatalogID:          catalogID,
		RequesterID:        requesterID,
		ComplianceAck:      reqData.ComplianceAck,
		NeedsPublicIP:      reqData.NeedsPublicIP,
		DataClassification: reqData.DataClassification,
		FormData:           reqData.FormData,
		CostCenter:         reqData.CostCenter,
		SourceIPWhitelist:  reqData.SourceIPWhitelist,
		ExpireAt:           reqData.ExpireAt,
	}

	if cat.CITypeID > 0 {
		ciID, err := s.ensureLinkedCI(ctx, tenantID, cat, reqData)
		if err != nil {
			return nil, err
		}
		newReq.CiID = ciID
	}

	// 4. Save
	created, err := s.repo.Create(ctx, newReq)
	if err != nil {
		s.logger.Errorw("Failed to create service request (linked ticket already created)", "error", err, "ticket_id", createdTicket.ID)
		return nil, common.NewInternalError("Failed to create service request", err)
	}

	// 5. Persist dynamic custom field values against the TICKET now, not the SR
	// (entity_type/entity_id 归属改成 ticket，这样工单详情页能像其他自定义字段一样直接展示)。
	if s.client != nil {
		if fieldValues := extractServiceRequestFieldValues(reqData.FormData); len(fieldValues) > 0 {
			if err := service.NewFieldValueService(s.client).CreateValues(ctx, tenantID, "service_catalog", catalogID, "ticket", createdTicket.ID, fieldValues); err != nil {
				s.logger.Warnw("Failed to persist service request custom field values", "error", err, "ticket_id", createdTicket.ID)
			}
		}
	}

	return created, nil
}
```

`Service` 结构体加一个字段 `ticketSvc TicketServiceInterface`（新定义一个只暴露 `CreateTicket` 的最小接口，避免直接依赖 `*service.TicketService` 具体类型造成循环/过重依赖）：

```go
// TicketServiceInterface 是 Create 需要的最小 Ticket 创建能力，用接口而非具体类型
// 避免 handlers/service_request 直接依赖 service.TicketService 的完整实现。
type TicketServiceInterface interface {
	CreateTicket(ctx context.Context, req *dto.CreateTicketRequest, tenantID int) (*ticket.Ticket, error)
}
```

（`ticket.Ticket` 是 `itsm-backend/domain/ticket` 或 `service/ticket_service.go` 里 `CreateTicket` 实际返回的类型——写代码前先 `grep -n "^func (s \*TicketService) CreateTicket" -A 3 itsm-backend/service/ticket_service.go` 确认精确的包路径和返回类型，不要猜。）

`NewService` 构造函数加一个参数 `ticketSvc TicketServiceInterface`，调用方（`internal/bootstrap/app.go` 或 `internal/container/container.go`，grep `service_request.NewService(` 确认调用点）要同步传入真实的 `TicketService` 实例。

新增两个小helper（domainReq 上取 title/reason 的兜底逻辑，从原来 handler.go 的 `normalizeCreateServiceRequest` 挪一部分过来，或者直接在 `ServiceRequest` 加两个方法）：

```go
// title/reason 这两个展示字段现在只是"创建 ticket 时的初始值"，不再持久化在 SR 表上——
// 从 FormData 兜底读，和 handler.go normalizeCreateServiceRequest 已经做的事保持一致。
func (r *ServiceRequest) title() string {
	if v, ok := r.FormData["title"].(string); ok {
		return v
	}
	return ""
}
func (r *ServiceRequest) reason() string {
	if v, ok := r.FormData["reason"].(string); ok {
		return v
	}
	return ""
}
```

（这两个 helper 需要 handler.go 在调 `Create` 前把 `req.Title`/`req.Reason` 塞进 `domainReq.FormData["title"]`/`["reason"]`——见 Step 5。）

**删除方法**：`ApplyApproval`、`checkEligibility`、`ListPendingApprovals`、`UpdateStatus`、`isValidServiceRequestOperationalTransition`。

`Get` 方法签名从 `(*ServiceRequest, []*ServiceRequestApproval, error)` 改为 `(*ServiceRequest, error)`（没有 approvals 要返回了）：

```go
func (s *Service) Get(ctx context.Context, id, tenantID int) (*ServiceRequest, error) {
	return s.repo.Get(ctx, id, tenantID)
}

// GetByTicketID 供 ticket 详情页查询关联的 SR 扩展数据（Task 2 前端用）。
// 找不到时返回的 error 用 ent.IsNotFound 判断——不是每个 ticket 都有关联 SR，
// 调用方（ticket handler）要能区分"这不是服务目录来源的 ticket"和真正的查询失败。
func (s *Service) GetByTicketID(ctx context.Context, ticketID, tenantID int) (*ServiceRequest, error) {
	return s.repo.GetByTicketID(ctx, ticketID, tenantID)
}
```

`List` 方法签名不变（`ListFilters` 已经没有 `Status` 字段，见 Step 3）。

`Update` 方法删除 `req.Status != SRStatusSubmitted` 那个前置状态检查（SR 自己没有 status 了；要不要根据关联 ticket 的状态限制编辑，这次不做，留给下一轮"审批收敛"处理——本任务只需要保证编译通过、行为不比之前更宽松到危险的地步，缺少这个检查是本任务已知的、可接受的简化），删除 `req.Title`/`req.Reason` 字段更新那两个 `if` 块（SR 不再有这两个字段）。

`Delete` 方法删除 `req.Status != SRStatusSubmitted && ...` 那个前置检查（同上，简化）。

`isServiceRequestAdmin`/`isServiceRequestOperator` 保留不变。

`serviceRequestSystemFormDataKeys`/`parseServiceRequestFieldValuesArray`/`extractServiceRequestFieldValues` 保留不变（这几个函数跟字段归属实体无关，是纯粹的 FormData 解析逻辑）。

- [ ] **Step 5: `handlers/service_request/handler.go` 重写**

`toDTO` 方法：删除 `approvals []*ServiceRequestApproval` 参数，删除返回体里 `Status`/`Title`/`Reason`/`CurrentLevel`/`TotalLevels`/`ApprovedAt`/`Approvals` 字段赋值，新增 `TicketID: req.TicketID`：

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
		Version:            req.Version,
		ProcessorID:        req.ProcessorID,
		StartedAt:          req.StartedAt,
		CompletedAt:        req.CompletedAt,
		CompletionNote:     req.CompletionNote,
		LastError:          req.LastError,
		CreatedAt:          req.CreatedAt,
		UpdatedAt:          req.UpdatedAt,
	}
	if req.ExpireAt != nil {
		t := *req.ExpireAt
		resp.ExpireAt = &t
	}
	return resp
}
```

`toDTOWithCustomFields` 里 `service.NewFieldValueService(client).ListValues(ctx, req.TenantID, "service_request", req.ID)` 改成 `ListValues(ctx, req.TenantID, "ticket", req.TicketID)`（自定义字段值现在挂在 ticket 上）；方法签名同步去掉 `approvals` 参数。

`Create` 方法：`domainReq` 构造时不再直接传 `Title`/`Reason`，改成塞进 `FormData`（配合 Step 4 的 `title()`/`reason()` helper）：

```go
	domainReq := &ServiceRequest{
		ComplianceAck:      req.ComplianceAck,
		NeedsPublicIP:      req.NeedsPublicIP,
		DataClassification: req.DataClassification,
		FormData:           req.FormData,
		CostCenter:         req.CostCenter,
		SourceIPWhitelist:  req.SourceIPWhitelist,
		ExpireAt:           expireAt,
	}
	if domainReq.FormData == nil {
		domainReq.FormData = map[string]interface{}{}
	}
	domainReq.FormData["title"] = req.Title
	domainReq.FormData["reason"] = req.Reason
```

后面 `h.service.Get(...)` 调用点同步去掉多余的 `approvals` 返回值接收；`h.toDTO(created, nil)`/`h.toDTOWithCustomFields(fullReq, approvals, ...)` 调用点同步去掉 `nil`/`approvals` 参数。

新增 `GetByTicket` 方法（供 Task 2 前端渲染 ticket 详情页的 SR 面板查询）：

```go
func (h *Handler) GetByTicket(c *gin.Context) {
	ticketIDStr := c.Param("ticketId")
	ticketID, err := strconv.Atoi(ticketIDStr)
	if err != nil {
		common.Fail(c, 1001, "Invalid ticket ID")
		return
	}
	tenantID := c.GetInt("tenant_id")

	req, err := h.service.GetByTicketID(c.Request.Context(), ticketID, tenantID)
	if err != nil {
		if ent.IsNotFound(err) {
			common.Fail(c, 404, "No service request linked to this ticket")
		} else {
			common.Fail(c, 5001, err.Error())
		}
		return
	}
	common.Success(c, h.toDTOWithCustomFields(req, h.service.Client()))
}
```

`itsm-backend/router/router.go` 里 `sr := tenant.(*gin.RouterGroup).Group("/service-requests")` 那个分组下加一行（放在 `sr.GET("/:id", ...)` 之前，参照同分组下已有的 `sr.GET("/me", ...)` 这个"静态路径优先于 `:id` 通配"的先例，Gin 的路由器能正确区分，不会跟 `/:id` 冲突）：

```go
sr.GET("/by-ticket/:ticketId", middleware.RequirePermission("service_request", "read"), config.ServiceRequestHandler.GetByTicket)
```

**删除方法**：`ApplyApproval`、`ListPending`（`UpdateStatus` 方法也删除，因为 Repository/Service 都没有对应能力了）。同时在 `router.go` 里删除这几行路由：`sr.GET("/approvals/pending", ...)`、`sr.PUT("/:id/status", ...)`、`sr.POST("/:id/approval", ...)`、`sr.POST("/:id/approvals", ...)`。

`Get`/`List`/`Update`/`Delete` 方法体里所有 `approvals` 相关的局部变量、`toDTO`/`toDTOWithCustomFields` 调用参数同步去掉。

`normalizeCreateServiceRequest` 里 `req.Title`/`req.Reason` 兜底逻辑不变（这两个字段现在只是"创建请求的输入"，不再是持久化字段，逻辑本身不用改）。

`normalizeServiceRequestStatus` 函数整个删除（没有 SR 状态可归一化了）。

- [ ] **Step 6: `handlers/service_request/repository_impl.go` 重写**

`toDomain` 方法删除 `Status`/`Title`/`Reason`/`CurrentLevel`/`TotalLevels`/`ApprovedAt` 字段映射，新增 `TicketID: req.TicketID`。`toDomainApproval` 方法和相关 import（`ent/servicerequestapproval`）整个删除。

`Create` 方法签名改为 `Create(ctx context.Context, req *ServiceRequest) (*ServiceRequest, error)`（不再接收 `approvals []*ServiceRequestApproval` 参数），方法体里创建 `ServiceRequestApproval` 记录那部分整个删除，`SetTicketID(req.TicketID)` 加到 ent builder 链上。

新增 `GetByTicketID` 方法：

```go
func (r *EntRepository) GetByTicketID(ctx context.Context, ticketID, tenantID int) (*ServiceRequest, error) {
	sr, err := r.client.ServiceRequest.Query().
		Where(servicerequest.TicketID(ticketID), servicerequest.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	return r.toDomain(sr), nil
}
```

`GetWithApprovals`、`UpdateStatus`、`GetApproval`、`UpdateApproval`、`UpdateRequestAndApproval`、`ListPendingApprovals` 方法整个删除（含 `ent/servicerequestapproval` 相关 import）。`Get`/`List`/`Update`/`Delete` 保留，方法体里去掉对已删字段的引用（具体字段名对照 Step 1/3）。

- [ ] **Step 7: `dto/service_dto.go` 瘦身**

`ServiceRequestResponse` 删除 `Status`/`Title`/`Reason`/`CurrentLevel`/`TotalLevels`/`ApprovedAt` 字段，新增 `TicketID int \`json:"ticketId"\``。`ServiceRequestApprovalResponse`、`ServiceRequestApprovalActionRequest` 两个类型整个删除。`CreateServiceRequestRequest`/`UpdateServiceRequestRequest` 里的 `Title`/`Reason` 字段保留不变（这两个还是创建/更新时的输入参数，只是不再持久化到 SR 表，创建时会被塞进 ticket；更新时暂不支持改标题——Update 方法本来就不再处理这两个字段，见 Step 4）。`UpdateServiceRequestStatusRequest` 类型整个删除。

- [ ] **Step 8: `service/provisioning_service.go` 改动**

`CreateTaskFromServiceRequest` 里：

```go
	sr, err := tx.ServiceRequest.Query().
		Where(servicerequest.ID(serviceRequestID), servicerequest.TenantID(tenantID)).
		First(ctx)
	if err != nil {
		return nil, fmt.Errorf("服务请求不存在")
	}
```

这段查询不变，但紧接着的：

```go
	if string(sr.Status) != "security_approved" {
		return nil, fmt.Errorf("当前状态不允许启动交付（需要 security_approved）")
	}
```

改成（需要新 import `itsm-backend/ent/processapprovaldecision`，`grep -n "processapprovaldecision" itsm-backend/ent -r` 先确认生成的包名和字段访问器是否正是这个拼法）：

```go
	approved, err := tx.ProcessApprovalDecision.Query().
		Where(
			processapprovaldecision.BusinessType("ticket"),
			processapprovaldecision.BusinessID(strconv.Itoa(sr.TicketID)),
			processapprovaldecision.Decision("approved"),
			processapprovaldecision.TenantID(tenantID),
		).
		Exist(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询审批状态失败: %w", err)
	}
	if !approved {
		return nil, fmt.Errorf("当前状态不允许启动交付（需要关联工单审批通过）")
	}
```

（`BusinessID` 字段在 `process_approval_decision` schema 里是 `field.String("business_id")`——是字符串类型，`strconv.Itoa(sr.TicketID)` 转换一下，具体字段类型执行前用 `grep -n "business_id" itsm-backend/ent/schema/process_approval_decision.go` 确认。）

方法末尾的：

```go
	// 回写 ServiceRequest 状态为 provisioning
	if err := tx.ServiceRequest.Update().
		Where(servicerequest.ID(serviceRequestID), servicerequest.TenantID(tenantID)).
		SetStatus("provisioning").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("更新服务请求状态失败: %w", err)
	}
```

整段删除（SR 没有 status 字段了，交付状态直接从 `ProvisioningTask.Status` 派生，见 Task 2 前端改动）。

- [ ] **Step 9: 补迁移文件**

`ls itsm-backend/migrations/ | tail -10` 看命名规则，照着建：

```sql
-- itsm-backend/migrations/<按现有规则命名>_service_request_delegates_to_ticket.sql
ALTER TABLE service_requests DROP COLUMN IF EXISTS status;
ALTER TABLE service_requests DROP COLUMN IF EXISTS title;
ALTER TABLE service_requests DROP COLUMN IF EXISTS reason;
ALTER TABLE service_requests DROP COLUMN IF EXISTS current_level;
ALTER TABLE service_requests DROP COLUMN IF EXISTS total_levels;
ALTER TABLE service_requests DROP COLUMN IF EXISTS current_approver;
ALTER TABLE service_requests DROP COLUMN IF EXISTS approved_at;
ALTER TABLE service_requests DROP COLUMN IF EXISTS approver_comment;
ALTER TABLE service_requests DROP COLUMN IF EXISTS approval_history;
DROP TABLE IF EXISTS service_request_approvals;
-- 旧的、归属在 entity_type='service_request' 下的自定义字段值是测试数据，直接清掉，
-- 新提交都会落在 entity_type='ticket' 下（见 handlers/service_request/service.go Create）。
DELETE FROM field_values WHERE entity_type = 'service_request';
```

跟 Task 10（`012_drop_service_catalog_item`）同样的方式在 `itsm-backend/migration/migrations.go` 里注册（`RegisteredMigrations` 追加一项，`GetMigrationSQL` 的 `switch` 里加对应 `case`，版本号接着上一个已注册版本往后编）；`RollbackSQL: ""`（不可逆迁移，跟 012 一致）。

- [ ] **Step 10: 编译，确认没有遗漏引用**

Run: `cd itsm-backend && go build ./... 2>&1 | tail -100`

预期会报错的地方（照着改，不在这个列表里但报错了的地方，说明前面步骤漏了，回去补）：
- `internal/bootstrap/app.go` 或 `internal/container/container.go`：`service_request.NewService(...)` 调用点要传入新增的 `ticketSvc` 参数
- `handlers/service_request/handler_test.go`/`service_test.go`/`handler_bpmn_bridge_test.go`：大量调用点要同步改（少 `approvals` 返回值、`ServiceRequestApproval` 类型不存在了等）——这三个测试文件本任务范围内要看着编译错误逐一修，不追求这一步就把测试逻辑写对，先让它编译过，测试断言修正在 Step 11。

Expected: 最终无输出（编译通过）。

- [ ] **Step 11: 改写测试，跑通**

`handlers/service_request/service_test.go`：删除审批相关测试用例（`ApplyApproval`/`ListPendingApprovals` 相关），新增：

```go
func TestService_Create_LinksTicketAndDelegatesFields(t *testing.T) {
	// 断言：Create 成功后返回的 ServiceRequest.TicketID > 0；
	// 用 TicketID 能查到一条 title/description 对应申请内容的 Ticket；
	// 该 Ticket 有一条 process_instance 记录（证明 BPMN 触发了）。
}

func TestService_GetByTicketID_ReturnsLinkedServiceRequest(t *testing.T) {
	// 断言：先 Create 一个 SR，再用返回的 TicketID 调 GetByTicketID，能查到同一条 SR
	// （catalog_id/cost_center 等字段一致）。
}
```

`handlers/service_request/handler_test.go`/`handler_bpmn_bridge_test.go`：删除 `TestHandler_ApplyApproval`/`TestHandler_ListPending` 一类的用例，其余用例里对 `Status`/`Title`/`CurrentLevel` 等字段的断言改成对应删掉或改成查关联 ticket。

`itsm-backend/tests/integration/service_catalog_fields_test.go`（Task 5 建的端到端测试，这次会话早前的工作）：如果里面断言了 SR 的 `status`/`customFields` 挂在 `entity_type=service_request` 下，同步改成通过 `ticketId` 查 ticket 详情、`entity_type=ticket` 下能查到自定义字段值。

`service/provisioning_service_test.go`（如果存在，`find itsm-backend/service -iname "provisioning_service_test.go"` 确认）：`CreateTaskFromServiceRequest` 的前置检查测试改成插入 `process_approval_decision` 记录而不是设置 `sr.Status`。

Run: `cd itsm-backend && gofmt -l . && go build ./... && go test ./handlers/service_request/... ./service/... ./tests/integration/... 2>&1 | tail -150`
Expected: `gofmt -l .` 无输出；build 无输出；测试全部 `ok`（除了本仓库已知的、跟本次改动无关的既有失败——密码策略校验、incident 状态流转测试）。

- [ ] **Step 12: Commit**

```bash
cd itsm-backend
git add -A
git commit -m "refactor(backend): delegate ServiceRequest status/workflow/approval to linked Ticket

- ServiceRequest slimmed down: drop status/title/reason/approval-progress
  fields, add ticket_id FK (1:1)
- ServiceRequestApproval table dropped entirely — approval now goes
  through the linked ticket's existing BPMN process_approval_decision
- Create() now creates a linked Ticket first (source=service_catalog,
  triggers the existing BPMN flow), then the slimmed ServiceRequest row
- custom field values for SR submissions now attach to entity_type=ticket
  instead of entity_type=service_request
- ProvisioningService.CreateTaskFromServiceRequest's precondition changed
  from sr.Status==security_approved to querying process_approval_decision
  for the linked ticket; status write-back removed (delivery state now
  derives from ProvisioningTask itself)"
```

---

### Task 2：前端——退休独立详情页，SR 面板并入 Ticket 详情页

**Files:**
- Delete: `itsm-frontend/src/app/(main)/service-requests/[id]/page.tsx`
- Delete: `itsm-frontend/src/components/service-request/ServiceRequestDetail.tsx`
- Modify: `itsm-frontend/src/app/(main)/my-requests/[requestId]/page.tsx`（改为重定向到 `/tickets/:ticketId`，不再自己渲染详情）
- Modify: `itsm-frontend/src/components/ticket/TicketDetail.tsx`（`source === 'service_catalog'` 时渲染 SR 面板）
- Modify: `itsm-backend/handlers/ticket` 或 `itsm-backend/service/ticket_service.go`（ticket 详情接口的响应加 `source` 字段——先 `grep -rn "ToTicketResponse\b" itsm-backend/service/ticket_service.go` 找到 mapper，加一行）
- Modify: `itsm-frontend/src/types/ticket.ts` 或对应类型文件（`Ticket` 类型加 `source?: string`）
- Create: `itsm-frontend/src/components/ticket/ServiceRequestPanel.tsx`（新组件，SR 专属信息 + 交付任务列表/操作）
- Modify: `itsm-frontend/src/lib/api/service-catalog-api.ts`（`getServiceRequest` 改用 `ticketId` 查或保留按 SR id 查但响应体不再有 status/title）
- Test: `itsm-frontend/src/components/ticket/__tests__/ServiceRequestPanel.test.tsx`（新建）

**Interfaces:**
- Consumes: Task 1 产出的 `dto.ServiceRequestResponse.ticketId`；已有的 `TicketApi.getTicket(id)`（返回体需要带上新增的 `source` 字段，这个任务里要去后端补上）
- Produces: `<ServiceRequestPanel ticketId={number} />` 组件，`TicketDetail.tsx` 在 `ticket.source === 'service_catalog'` 时挂载

- [ ] **Step 1: 后端 Ticket 响应体加 `source` 字段**

找到 ticket 详情/列表的 DTO mapper（`grep -n "func ToTicketResponse\|func.*ToTicketResponseWithCustomFields" itsm-backend/service/ticket_service.go`），在返回的 struct 字面量里加一行 `Source: t.Source,`（Ent 字段访问器是 `t.Source`，因为 schema 里字段名是 `source`）。`dto.TicketResponse`（或对应类型，`grep -n "type TicketResponse struct" itsm-backend/dto/ticket_dto.go`）加一个字段 `Source string \`json:"source,omitempty"\``。

Run: `cd itsm-backend && go build ./... && go test ./service/... ./handlers/ticket/... 2>&1 | tail -60`
Expected: 编译通过，既有测试不受影响（新加字段是 additive，不改变现有断言）。

- [ ] **Step 2: 读 `ServiceRequestDetail.tsx` 和 `my-requests/[requestId]/page.tsx` 现在展示的所有信息，列出要挪到新面板的内容**

这一步不写代码，是核对清单——执行前把这两个文件现有渲染的字段/操作全部列出来，确认 Step 3 的新面板没有漏掉：`ServiceRequestDetail.tsx` 有服务名称/申请原因/成本中心/数据分类/公网IP/自定义字段/formData 原始 JSON、审批时间轴+批准拒绝按钮（这部分不用挪，ticket 详情页已有审批 UI）；`my-requests/[requestId]/page.tsx` 有状态标签、当前级别、关联CI、标题、原因、数据分级、成本中心、审批记录表格、交付任务列表（`ProvisioningTask`）、"开始交付"按钮。

- [ ] **Step 3: 新建 `ServiceRequestPanel.tsx`**

```tsx
'use client';

import React, { useEffect, useState } from 'react';
import { Card, Descriptions, Tag, Table, Button, message, Empty } from 'antd';
import { PlayCircle } from 'lucide-react';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { serviceRequestAPI } from '@/lib/api/service-request-api';
import type { ProvisioningTask } from '@/lib/api/service-request-api';

interface ServiceRequestPanelProps {
  ticketId: number;
}

// 服务目录来源的工单，在工单详情页里额外展示的补充信息面板——
// 之前分散在两个独立详情页（/service-requests/[id]、/my-requests/[requestId]）里的
// SR 专属字段和交付任务展示，这次统一收到这里，不再维护两份重复代码。
export default function ServiceRequestPanel({ ticketId }: ServiceRequestPanelProps) {
  const [loading, setLoading] = useState(true);
  const [request, setRequest] = useState<any>(null);
  const [tasks, setTasks] = useState<ProvisioningTask[]>([]);
  const [starting, setStarting] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      const data = await ServiceCatalogApi.getServiceRequestByTicketId(ticketId);
      setRequest(data);
      if (data?.id) {
        const taskList = await serviceRequestAPI.listProvisioningTasks(data.id);
        setTasks(taskList || []);
      }
    } catch {
      // 这个 ticket 不是服务目录来源，或者查询失败——不渲染面板即可，不当错误处理
      setRequest(null);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
     
  }, [ticketId]);

  const handleStartProvisioning = async () => {
    if (!request?.id) return;
    setStarting(true);
    try {
      await serviceRequestAPI.startProvisioning(request.id);
      message.success('已开始交付');
      load();
    } catch (e: any) {
      message.error(e?.message || '启动交付失败');
    } finally {
      setStarting(false);
    }
  };

  if (loading) return null;
  if (!request) return null;

  const latestTask = tasks[tasks.length - 1];

  return (
    <Card title="服务申请信息" style={{ marginBottom: 16 }}>
      <Descriptions column={2} bordered size="small">
        <Descriptions.Item label="成本中心">{request.costCenter || '-'}</Descriptions.Item>
        <Descriptions.Item label="数据分类">{request.dataClassification || 'internal'}</Descriptions.Item>
        <Descriptions.Item label="需要公网IP">{request.needsPublicIp ? '是' : '否'}</Descriptions.Item>
        <Descriptions.Item label="到期时间">
          {request.expireAt ? new Date(request.expireAt).toLocaleString() : '-'}
        </Descriptions.Item>
      </Descriptions>

      <Card type="inner" title="资源交付" style={{ marginTop: 16 }}>
        {tasks.length === 0 ? (
          <Empty description="尚未开始交付">
            <Button
              type="primary"
              icon={<PlayCircle size={14} />}
              loading={starting}
              onClick={handleStartProvisioning}
            >
              开始交付
            </Button>
          </Empty>
        ) : (
          <Table
            size="small"
            rowKey="id"
            dataSource={tasks}
            pagination={false}
            columns={[
              { title: 'Provider', dataIndex: 'provider' },
              { title: '资源类型', dataIndex: 'resourceType' },
              {
                title: '状态',
                dataIndex: 'status',
                render: (s: string) => <Tag>{s}</Tag>,
              },
              { title: '更新时间', dataIndex: 'updatedAt', render: (t: string) => new Date(t).toLocaleString() },
            ]}
          />
        )}
      </Card>
    </Card>
  );
}
```

`ServiceCatalogApi.getServiceRequestByTicketId`（新方法，加进 `src/lib/api/service-catalog-api.ts`，调用 Task 1 Step 5 新加的 `GET /api/v1/service-requests/by-ticket/:ticketId`）：

```ts
/**
 * 按关联的 ticketId 查服务请求（供工单详情页的 ServiceRequestPanel 用）
 */
static async getServiceRequestByTicketId(ticketId: number): Promise<any> {
  const resp = await httpClient.get<any>(`/api/v1/service-requests/by-ticket/${ticketId}`);
  return ServiceCatalogApi.toServiceRequest(resp);
}
```

（照 `getServiceRequest(id)` 现有的写法抄一份路径换掉，`toServiceRequest` 复用同一个响应体转换函数。）

- [ ] **Step 4: `TicketDetail.tsx` 挂载面板**

`grep -n "ApprovalWorkflowPanel" itsm-frontend/src/components/ticket/TicketDetail.tsx` 找到现有面板挂载的位置，在其上方（或任意合理位置）加：

```tsx
{ticket?.source === 'service_catalog' && <ServiceRequestPanel ticketId={ticket.id} />}
```

import 加 `import ServiceRequestPanel from './ServiceRequestPanel';`（同目录）。

- [ ] **Step 5: 退休旧页面**

`itsm-frontend/src/app/(main)/service-requests/[id]/page.tsx` 改为纯重定向（不删除路由文件，因为可能有旧书签/外部链接指向它）：

```tsx
'use client';

import React, { useEffect } from 'react';
import { useParams, useRouter } from 'next/navigation';
import { Spin } from 'antd';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';

// 旧的独立 SR 详情页已退休，统一到 /tickets/:ticketId + ServiceRequestPanel。
// 保留这个路由文件做重定向，兼容可能存在的旧书签/外部链接。
export default function ServiceRequestDetailRedirectPage() {
  const { id } = useParams() as { id: string };
  const router = useRouter();

  useEffect(() => {
    (async () => {
      try {
        const data = await ServiceCatalogApi.getServiceRequest(Number(id));
        if (data?.ticketId) {
          router.replace(`/tickets/${data.ticketId}`);
          return;
        }
      } catch {
        // fall through to tickets list
      }
      router.replace('/tickets');
    })();
     
  }, [id]);

  return <Spin size="large" style={{ display: 'block', margin: '80px auto' }} />;
}
```

删除 `itsm-frontend/src/components/service-request/ServiceRequestDetail.tsx`（不再被引用）。

`itsm-frontend/src/app/(main)/my-requests/[requestId]/page.tsx` 同样改为重定向（复用上面的逻辑，`requestId` 换成 `params.requestId`）。

- [ ] **Step 6: 类型检查 + 测试**

Run: `cd itsm-frontend && npx tsc --noEmit 2>&1 | tail -80`
Expected: 无类型错误（`ServiceRequest`/`Ticket` 相关类型定义如果有引用被删字段的地方，这一步会暴露，回去改类型文件）。

写 `ServiceRequestPanel.test.tsx`：断言非 `service_catalog` 来源时不渲染（mock API 返回 404/无数据）；`service_catalog` 来源且有交付任务时渲染任务表格；无交付任务时渲染"开始交付"按钮，点击调用 `startProvisioning`。

Run: `cd itsm-frontend && npx jest ServiceRequestPanel 2>&1 | tail -60`
Expected: 新增测试通过。

- [ ] **Step 7: Commit**

```bash
cd itsm-frontend
git add -A
git commit -m "refactor(frontend): retire standalone SR detail pages, fold into ticket detail

- Delete ServiceRequestDetail.tsx and its two duplicate custom-field
  rendering implementations (/service-requests/[id], /my-requests/[requestId])
- Add ServiceRequestPanel to TicketDetail.tsx, shown when
  ticket.source === 'service_catalog' — carries SR-specific fields and
  provisioning task list/start-provisioning action
- Old routes redirect to /tickets/:ticketId for backward-compat bookmarks"
```

---

### Task 3：前端——提交页跳转目标、我的申请列表调整

**Files:**
- Modify: `itsm-frontend/src/app/(main)/service-catalog/request/[id]/page.tsx`
- Modify: `itsm-frontend/src/app/(main)/my-requests/page.tsx`
- Modify: `itsm-frontend/src/lib/api/service-catalog-api.ts`（`createServiceRequest` 返回类型加 `ticketId`）
- Modify: `itsm-frontend/src/types/biz/service-request.ts`（同步删掉 `status`/`title`/`reason`/`currentLevel`/`totalLevels` 等已经不存在的字段，加 `ticketId`）

**Interfaces:**
- Consumes: Task 1 的 `dto.ServiceRequestResponse.ticketId`

- [ ] **Step 1: 提交成功后跳转目标改成 ticket 详情页**

`grep -n "router.push('/my-requests')" "itsm-frontend/src/app/(main)/service-catalog/request/[id]/page.tsx"` 找到提交成功的跳转逻辑，改成：

```tsx
const created = await ServiceCatalogApi.createServiceRequest(payload);
message.success('申请已提交，等待审批');
router.push(`/tickets/${created.ticketId}`);
```

（`created.ticketId` 要求 `ServiceCatalogApi.createServiceRequest` 的返回类型里有这个字段——检查 `src/lib/api/service-catalog-api.ts` 里 `createServiceRequest` 方法当前的返回类型声明，加上 `ticketId: number`。）

- [ ] **Step 2: `/my-requests` 列表页调整**

`grep -n "getServiceRequests\|columns" "itsm-frontend/src/app/(main)/my-requests/page.tsx"` 看现有列表列定义。原来展示"状态"这一列（`request.status`）现在数据源里没有了——列表接口 `ServiceCatalogApi.getServiceRequests` 返回的每条记录也没有 `status`/`title` 了（Task 1 删了）。这一列改成需要联查 ticket 状态才能展示：最简单的处理方式是列表接口响应体里除了 SR 自己的字段外，也带上关联 ticket 的 `title`/`status`（后端 `handler.go` 的 `List` 方法内部改成对每条记录批量查一次关联 ticket——参照本仓库已有的"List 场景批量加载避免 N+1"先例，比如 `handlers/service_catalog/service.go` 的 `ListDefinitionsForEntities`，不要逐条查）。

这一步具体是：Task 1 的 `dto.ServiceRequestResponse` 加 `ticketTitle string`/`ticketStatus string`（列表展示用的冗余字段，接口层面允许，不是持久化字段），`handlers/service_request/service.go` 的 `List` 方法批量查一次关联 ticket 的 title/status 填进去。

点击列表项跳转目标从 `/my-requests/:id` 改成 `/tickets/:ticketId`。

Run: `cd itsm-frontend && npx tsc --noEmit && npx jest my-requests 2>&1 | tail -60`
Expected: 类型检查/测试通过。

- [ ] **Step 3: Commit**

```bash
cd itsm-frontend
git add -A
git commit -m "refactor(frontend): point service-request flows at ticket detail page

- Submission success redirects to /tickets/:ticketId instead of /my-requests
- /my-requests list now shows linked ticket title/status (batch-loaded,
  no N+1) and links into ticket detail"
```

---

### Task 4：最终验证

**Files:** 无改动，纯验证。

- [ ] **Step 1: 后端全量测试 + 格式检查**

Run: `cd itsm-backend && gofmt -l . && go build ./... && go test ./... 2>&1 | tail -150`
Expected: `gofmt -l .` 无输出；build 无输出；测试全部 `ok`（除本仓库已知的、跟本次改动无关的既有失败）。

- [ ] **Step 2: 前端类型检查 + 全量测试**

Run: `cd itsm-frontend && npx tsc --noEmit && npx jest 2>&1 | tail -80`
Expected: 无类型错误；Jest 全部通过。

- [ ] **Step 3: 手动跑一遍完整链路**

在本地环境走一遍：管理员在 `/admin/service-catalogs` 建服务（保留自定义字段）→ 员工在 `/service-catalog` 找到该服务提交申请 → 提交成功后应该跳到 `/tickets/:id` 而不是旧的 SR 详情页 → 工单详情页能看到"服务申请信息"面板（成本中心/数据分类等）和自定义字段的值（走的是 `entity_type=ticket` 这条路径）→ 工单详情页原有的审批面板对这条 service_catalog 来源的工单一样能用 → 审批通过后，在面板里点"开始交付"，能成功创建交付任务并展示状态 → `/my-requests` 列表能看到这条申请，点进去跳到同一个工单详情页。

- [ ] **Step 4: Commit（如果 Step 1-3 发现并修复了问题）**

如果前几步全部一次通过，这一步不需要新 commit。如果发现问题并修复，正常提交，commit message 具体描述修的是什么。

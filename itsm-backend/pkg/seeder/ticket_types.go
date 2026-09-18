package seeder

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/tickettype"
	creation "itsm-backend/handlers/common/workitemcreation"
)

type ticketTypeDefault struct {
	Code, Name, Description, Icon, Color string
}

// defaultTicketTypes is the sole product catalog for bootstrap and targeted repair.
func defaultTicketTypes() []ticketTypeDefault {
	return []ticketTypeDefault{
		{"k8s_scale", "K8S扩缩容", "Kubernetes容器集群扩容或缩容请求", "Container", "#1890ff"},
		{"ddl_execute", "DDL执行", "数据库表结构变更、索引创建等DDL操作", "Database", "#722ed1"},
		{"data_export", "数据导出", "从数据库或系统导出数据", "Download", "#13c2c2"},
		{"vm_apply", "虚拟机申请", "申请新的虚拟机资源", "Desktop", "#2f54eb"},
		{"account_apply", "账号申请", "申请系统账号、VPN账号、堡垒机账号等", "User", "#52c41a"},
		{"gitlab_repo_apply", "GitLab代码仓库申请", "申请创建新的GitLab代码仓库", "Code", "#fa541c"},
		{"domain_apply", "域名申请", "申请新的域名或域名解析变更", "Global", "#eb2f96"},
		{"firewall_apply", "防火墙规则申请", "申请开放或变更防火墙端口规则", "Safety", "#fa8c16"},
		{"app_apply", "应用申请", "申请在K8S集群中部署新应用服务", "Appstore", "#1890ff"},
		{"project_apply", "项目申请", "申请创建新项目或项目空间", "Project", "#722ed1"},
		{"db_account_apply", "数据库账号申请", "申请数据库读写账号、只读账号等", "Key", "#faad14"},
		{"general", "其他工单", "通用工单类型，用于不属于以上分类的请求", "FileText", "#8c8c8c"},
	}
}

func createDefaultTicketType(ctx context.Context, client *ent.Client, item ticketTypeDefault, tenantID, actorID int) (*ent.TicketType, error) {
	now := time.Now()
	return client.TicketType.Create().SetCode(item.Code).SetName(item.Name).SetDescription(item.Description).
		SetIcon(item.Icon).SetColor(item.Color).SetStatus("active").SetApprovalEnabled(false).SetSLAEnabled(false).
		SetAutoAssignEnabled(false).SetAssignmentRules([]interface{}{}).SetNotificationConfig(map[string]interface{}{}).
		SetPermissionConfig(map[string]interface{}{}).SetCreatedBy(int64(actorID)).SetTenantID(int64(tenantID)).
		SetCreatedAt(now).SetUpdatedAt(now).SetUsageCount(0).Save(ctx)
}

func matchesDefaultTicketType(row *ent.TicketType, item ticketTypeDefault) bool {
	return row.Code == item.Code && row.Name == item.Name && row.Description == item.Description &&
		row.Icon == item.Icon && row.Color == item.Color && row.Status == "active" &&
		!row.ApprovalEnabled && !row.SLAEnabled && row.DefaultSLAID == 0 && !row.AutoAssignEnabled &&
		len(row.AssignmentRules) == 0 && len(row.NotificationConfig) == 0 && len(row.PermissionConfig) == 0
}

type TicketTypeInitializationResult struct {
	TenantID       int      `json:"tenantId"`
	ActorID        int      `json:"actorId"`
	Applied        bool     `json:"applied"`
	ManifestSHA256 string   `json:"manifestSha256"`
	CreateCodes    []string `json:"createCodes"`
	PreservedCodes []string `json:"preservedCodes"`
	UnmanagedCodes []string `json:"unmanagedCodes"`
}

// InitializeTicketTypes repairs only the product subtype catalog for an existing
// tenant. Plan uses a read-only transaction. Apply checks current authorization
// and all conflicts again, then commits missing types and its audit atomically.
// It neither bootstraps identity nor changes initialization/migration ledgers.
func (s *Seeder) InitializeTicketTypes(ctx context.Context, tenantID, actorID int, apply bool) (*TicketTypeInitializationResult, error) {
	if s == nil || s.client == nil || tenantID <= 0 || actorID <= 0 {
		return nil, fmt.Errorf("existing tenant and actor are required")
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != tenantID {
		return nil, fmt.Errorf("ticket type initialization tenant context mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: !apply})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, nil, actorID, tenantID)
	if err != nil {
		return nil, err
	}
	if actor.TenantID != tenantID {
		return nil, fmt.Errorf("ticket type initialization requires a native tenant administrator")
	}
	identity := creation.Identity{TenantID: tenantID, ActorID: actorID, Role: authorization.EffectiveSessionRole(actor)}
	if err := authorization.RequireCurrentPermission(ctx, tx, identity, "system_config", "update"); err != nil {
		return nil, err
	}
	defaults := defaultTicketTypes()
	payload, err := json.Marshal(defaults)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	result := &TicketTypeInitializationResult{
		TenantID: tenantID, ActorID: actorID, ManifestSHA256: hex.EncodeToString(digest[:]),
		CreateCodes: []string{}, PreservedCodes: []string{}, UnmanagedCodes: []string{},
	}
	rows, err := tx.TicketType.Query().Where(tickettype.TenantIDEQ(int64(tenantID))).All(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]*ent.TicketType, len(rows))
	for _, row := range rows {
		if existing[row.Code] != nil {
			return nil, fmt.Errorf("ticket type conflict: duplicate code %s", row.Code)
		}
		existing[row.Code] = row
	}
	missing := []ticketTypeDefault{}
	for _, item := range defaults {
		row := existing[item.Code]
		if row == nil {
			missing = append(missing, item)
			result.CreateCodes = append(result.CreateCodes, item.Code)
		} else if !matchesDefaultTicketType(row, item) {
			return nil, fmt.Errorf("ticket type conflict: existing code %s differs from product defaults", item.Code)
		} else {
			result.PreservedCodes = append(result.PreservedCodes, item.Code)
		}
		delete(existing, item.Code)
	}
	for code := range existing {
		result.UnmanagedCodes = append(result.UnmanagedCodes, code)
	}
	sort.Strings(result.CreateCodes)
	sort.Strings(result.PreservedCodes)
	sort.Strings(result.UnmanagedCodes)
	if !apply {
		if err := tx.Rollback(); err != nil {
			return nil, err
		}
		return result, nil
	}
	for _, item := range missing {
		if _, err := createDefaultTicketType(ctx, tx.Client(), item, tenantID, actorID); err != nil {
			return nil, fmt.Errorf("create ticket type %s: %w", item.Code, err)
		}
	}
	result.Applied = true
	payload, err = json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if err := tx.AuditLog.Create().SetTenantID(tenantID).SetUserID(actorID).SetResource("ticket_type").
		SetAction("ticket_types.initialize").SetPath("initialization:ticket_types").SetMethod("COMMAND").
		SetRequestBody(string(payload)).Exec(ctx); err != nil {
		return nil, fmt.Errorf("audit ticket type initialization: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

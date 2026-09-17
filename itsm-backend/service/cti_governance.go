package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/systemconfig"
	"itsm-backend/ent/ticket"
)

// CTI 治理的受控启用记录。
//
// 位置：既有 system_configs 的保留键，每租户唯一（迁移 048 建立的局部唯一索引是并发防线）。
// 语义：
//   - 从未配置 = 未启用（零值），普通报障与旧目录保持既有行为；
//   - 读取失败 / 重复行 / 非法值 => 明确错误，绝不默认为“关闭”；
//   - effectiveFrom 在首次启用完成门禁时写入，之后不可修改（由 B2 的受控激活路径保证）。
const CTIGovernanceConfigKey = "cti_governance_v1"

// CTIGovernance 是启用记录的结构化投影。
type CTIGovernance struct {
	EffectiveFrom      *time.Time
	CatalogEnforced    bool
	CompletionEnforced bool
}

type ctiGovernanceValue struct {
	CatalogEnforced    *bool   `json:"catalogEnforced"`
	CompletionEnforced *bool   `json:"completionEnforced"`
	EffectiveFrom      *string `json:"effectiveFrom"`
}

// CTI 完成质量的受控动作矩阵。
//
// 语义（设计 §6）：
//   - true  = 该专业完成动作要求完整三级分类；
//   - false = 已知动作但刻意不加门禁（Incident resolve、取消/重开、目录任务等）；
//   - 不在矩阵内的 class/action 一律失败，绝不“默认通过”。
//
// 专业完成动作仍由各专业拥有者定义状态与权限；这里只回答“是否需要分类完整”。
var ctiCompletionActions = map[string]map[string]bool{
	"generic": {
		"resolve":  true,
		"close":    true,
		"cancel":   false, // 取消不是完成，沿专业终止命令处理
		"reopen":   false, // 重开后回到处理中，恢复事实由专业命令记录
		"pending":  false,
		"assign":   false, // 分派不改变完成状态
		"escalate": false,
		"update":   false,
	},
	"incident": {
		"assign":      false,
		"escalate":    false,
		"acknowledge": false,
		"start":       false,
		"resolve":     false, // 恢复服务优先：resolve 不加硬门禁
		"close":       true,  // 关闭需要完整分类
		"reopen":      false,
		"cancel":      false,
		"pending":     false,
	},
	"problem": {
		"investigate":       false,
		"select_resolution": false,
		"verify_resolution": false,
		"resolve":           false,
		"close":             true,
		"reopen":            false,
		"cancel":            false,
	},
	"change_request": {
		"submit":         false,
		"assess":         false,
		"authorize":      false,
		"schedule":       false,
		"implement":      false,
		"record_outcome": false,
		"review":         false,
		"close":          true,
		"cancel":         false,
	},
	"service_request_item": {
		"close":     true,
		"cancel":    false,
		"delivered": false, // 交付确认属于申请侧证据，不在此改写
		"fulfill":   false,
	},
	"catalog_task": {
		// 目录任务不新增完成门禁，保持既有专业语义。
		"close":   false,
		"cancel":  false,
		"fulfill": false,
	},
}

// RequiresCTICompletion 判断某个专业完成动作是否需要完整三级分类。
//
//   - 未启用完成门禁，或启用但缺少截止时间（损坏配置）=> 明确行为：
//     未启用返回 false；启用但缺截止时间返回错误，不误判为“关闭”；
//   - 记录创建时间 >= 截止时间 => 适用（相等也适用，截止前 1ns 不适用）；
//   - 未知 class/action => 错误（fail closed）。
//
// createdAt 必须来自服务端持久化时间戳，避免客户端自报时间绕过门禁。
func RequiresCTICompletion(policy CTIGovernance, createdAt time.Time, recordClass, action string) (bool, error) {
	classActions, known := ctiCompletionActions[strings.TrimSpace(recordClass)]
	if !known {
		return false, fmt.Errorf("unknown record class %q for CTI completion", recordClass)
	}
	gated, knownAction := classActions[strings.TrimSpace(action)]
	if !knownAction {
		return false, fmt.Errorf("unknown completion action %q for record class %q", action, recordClass)
	}
	if !policy.CompletionEnforced {
		return false, nil
	}
	if policy.EffectiveFrom == nil {
		return false, errors.New("CTI completion is enforced without an effective cutoff")
	}
	if gated {
		return !createdAt.Before(*policy.EffectiveFrom), nil
	}
	// 已知但不门禁的动作不需要截止时间比较。
	return false, nil
}

// ReadCTIGovernance 在调用方事务内读取启用记录。
// catalogEnforced 控制目录发布/申请是否强制完整三级分类；completionEnforced 控制专业完成门禁。
func ReadCTIGovernance(ctx context.Context, tx *ent.Tx, tenantID int) (CTIGovernance, error) {
	if tx == nil {
		return CTIGovernance{}, errors.New("CTI governance requires the owning transaction")
	}
	if tenantID <= 0 {
		return CTIGovernance{}, ErrCTIPathOutsideTenant
	}
	rows, err := tx.SystemConfig.Query().
		Where(systemconfig.TenantIDEQ(tenantID), systemconfig.KeyEQ(CTIGovernanceConfigKey), systemconfig.DeletedAtIsNil()).
		All(ctx)
	if err != nil {
		return CTIGovernance{}, fmt.Errorf("could not read CTI governance record: %w", err)
	}
	switch len(rows) {
	case 0:
		return CTIGovernance{}, nil
	case 1:
	default:
		return CTIGovernance{}, fmt.Errorf("CTI governance record is duplicated for tenant %d", tenantID)
	}
	raw := strings.TrimSpace(rows[0].Value)
	if raw == "" {
		return CTIGovernance{}, errors.New("CTI governance record has no value")
	}
	var value ctiGovernanceValue
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return CTIGovernance{}, fmt.Errorf("CTI governance record is not valid JSON: %w", err)
	}
	governance := CTIGovernance{}
	if value.CatalogEnforced != nil {
		governance.CatalogEnforced = *value.CatalogEnforced
	}
	if value.CompletionEnforced != nil {
		governance.CompletionEnforced = *value.CompletionEnforced
	}
	if value.EffectiveFrom != nil {
		if strings.TrimSpace(*value.EffectiveFrom) == "" {
			return CTIGovernance{}, errors.New("CTI governance effectiveFrom must not be empty")
		}
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*value.EffectiveFrom))
		if err != nil {
			return CTIGovernance{}, fmt.Errorf("CTI governance effectiveFrom is not RFC3339: %w", err)
		}
		effective := parsed.UTC()
		governance.EffectiveFrom = &effective
	}
	return governance, nil
}

// CTIGovernanceForClient 供只读判断使用；返回错误时不降级为“未启用”。
func (s *TicketCategoryService) CTIGovernanceForClient(ctx context.Context, client *ent.Client, tenantID int) (CTIGovernance, error) {
	if client == nil {
		return CTIGovernance{}, errors.New("CTI governance requires a client")
	}
	tx, err := client.Tx(ctx)
	if err != nil {
		return CTIGovernance{}, err
	}
	defer func() { _ = tx.Rollback() }()
	return ReadCTIGovernance(ctx, tx, tenantID)
}

// ErrCTICompletionRequired 表示完成动作因分类不完整被拒绝（fail closed）。
// 调用方用 errors.Is 区分「业务拒绝」与基础设施故障，并翻译成各自专业的错误类型。
var ErrCTICompletionRequired = errors.New("work item completion requires a complete three-level classification")

// RequireWorkItemCTICompletion 在调用方事务内判定并校验完成门禁。
//
// 参数来自调用方事务中读取的权威行（服务端持久化的 createdAt 与最深节点），
// 因此客户端无法通过自报时间或自报分类绕过门禁。
//   - nil：允许完成（未启用，或动作不在矩阵内，或分类已完整）；
//
// 完整性只要求「路径存在且为三级」，不要求当前启用：停用节点只禁止新选择。
//   - ErrCTICompletionRequired：必须拒绝；
//   - 其它错误：基础设施/配置故障，必须整笔失败而不是当作允许。
func RequireWorkItemCTICompletion(ctx context.Context, tx *ent.Tx, tenantID int, recordClass, action string, createdAt time.Time, categoryID int) error {
	governance, err := ReadCTIGovernance(ctx, tx, tenantID)
	if err != nil {
		return err
	}
	needed, err := RequiresCTICompletion(governance, createdAt, recordClass, action)
	if err != nil {
		return err
	}
	if !needed {
		return nil
	}
	return requireCompleteClassification(ctx, tx, tenantID, categoryID, recordClass, action)
}

func requireCompleteClassification(ctx context.Context, tx *ent.Tx, tenantID, categoryID int, recordClass, action string) error {
	if categoryID <= 0 {
		return fmt.Errorf("%w: %s/%s has no classification", ErrCTICompletionRequired, recordClass, action)
	}
	path, err := NewTicketCategoryService(tx.Client()).ProjectCTIPath(ctx, tx, tenantID, categoryID)
	if err != nil {
		switch {
		case errors.Is(err, ErrCTICategoryNotFound), errors.Is(err, ErrCTIPathOutsideTenant),
			errors.Is(err, ErrCTIPathHierarchy), errors.Is(err, ErrCTIPathTooDeep):
			// 结构不可用（跨租户/层级损坏）一律视为分类不完整，不泄露存在性。
			return fmt.Errorf("%w: %s/%s classification is unavailable", ErrCTICompletionRequired, recordClass, action)
		default:
			return fmt.Errorf("could not project classification for completion: %w", err)
		}
	}
	if len(path) < CTIMaxDepth {
		return fmt.Errorf("%w: %s/%s classification has %d level(s)", ErrCTICompletionRequired, recordClass, action, len(path))
	}
	// 刻意不检查启用状态：停用只禁止“新选择”，分类停用前已合法引用的在途单必须仍可完成，
	// 否则停用一个节点会永久卡死历史工单。
	return nil
}

// EnforceWorkItemCompletionCTI 是专业命令的共同适配层：把门禁结论翻译成专业命令可直接返回的错误。
//
//   - 业务拒绝 → common.NewValidationError（调用方按各自专业语义返回 400 类错误）；
//   - 策略/基础设施故障 → 原样返回，整笔事务失败，绝不降级为允许。
func EnforceWorkItemCompletionCTI(ctx context.Context, tx *ent.Tx, tenantID int, workItem *ent.Ticket, action string) error {
	if workItem == nil {
		return errors.New("CTI completion requires the work item loaded in the owning transaction")
	}
	err := RequireWorkItemCTICompletion(ctx, tx, tenantID, workItem.RecordClass, action, workItem.CreatedAt, workItem.CategoryID)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrCTICompletionRequired) {
		return common.NewValidationError(err.Error(), err)
	}
	return err
}

// CTIGovernanceUpdate 是受控启用的输入。EffectiveFrom 刻意不在此结构内：
// 截止时间只能由第一次启用派生，任何调用方都无权直接设置或修改。
type CTIGovernanceUpdate struct {
	CatalogEnforced    bool
	CompletionEnforced bool
}

// CTIGovernanceResult 是受控启用的结果。
type CTIGovernanceResult struct {
	Governance CTIGovernance
	// InFlightWithoutClassification 是恢复启用时“截止后仍在途且完全没有分类”的工单数下界，
	// 供运营在恢复前盘点需要补齐的记录。真实的不完整集合还包括只有部分层级的记录。
	InFlightWithoutClassification int
	// Applied 表示本次调用是否真的改变了启用状态（幂等重放时为 false，不重复写审计）。
	Applied bool
}

// SetCTIGovernance 受控启用/暂停 CTI 门禁。
//
// 语义：
//   - 第一次启用时写入 effectiveFrom（服务端时间），之后不可修改，只能启停布尔值；
//   - 暂停不撤销已完成动作，也不清除 effectiveFrom，因此恢复不会把在途单变成“新单”；
//   - 行锁 + 唯一索引保证并发启用只有一个成功；
//   - 同事务写审计（actor/source/前后值），审计失败整笔回滚。
func (s *SystemConfigService) SetCTIGovernance(ctx context.Context, tenantID, actorID int, source string, update CTIGovernanceUpdate) (CTIGovernanceResult, error) {
	var result CTIGovernanceResult
	if s == nil || s.client == nil {
		return result, errors.New("CTI governance activation requires a client")
	}
	if tenantID <= 0 || actorID <= 0 || strings.TrimSpace(source) == "" {
		return result, errors.New("CTI governance activation requires tenant, actor and source")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := ReadCTIGovernance(ctx, tx, tenantID)
	if err != nil {
		return result, err
	}
	next := current
	next.CatalogEnforced = update.CatalogEnforced
	next.CompletionEnforced = update.CompletionEnforced
	now := time.Now().UTC()
	enabling := update.CatalogEnforced || update.CompletionEnforced
	if enabling && next.EffectiveFrom == nil {
		cutoff := now
		next.EffectiveFrom = &cutoff
	}
	if current.CatalogEnforced == next.CatalogEnforced && current.CompletionEnforced == next.CompletionEnforced &&
		sameInstant(current.EffectiveFrom, next.EffectiveFrom) {
		return CTIGovernanceResult{Governance: current}, nil
	}

	if err := writeCTIGovernanceTx(ctx, tx, tenantID, actorID, source, next); err != nil {
		return result, err
	}
	if enabling && current.EffectiveFrom != nil && !current.CatalogEnforced && !current.CompletionEnforced && next.EffectiveFrom != nil {
		// 从暂停恢复：给出需要补齐的在途规模下界。
		count, err := tx.Ticket.Query().
			Where(ticket.TenantIDEQ(tenantID), ticket.CreatedAtGTE(*next.EffectiveFrom), ticket.CategoryIDIsNil()).
			Where(ticket.StatusNotIn("closed", "cancelled")).
			Count(ctx)
		if err != nil {
			return result, err
		}
		result.InFlightWithoutClassification = count
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	result.Governance = next
	result.Applied = true
	return result, nil
}

func sameInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// writeCTIGovernanceTx 在调用方事务内写入启用记录并留下审计。
// 已存在时先做一次行更新（PostgreSQL 下即行锁），保证并发启用被串行化。
func writeCTIGovernanceTx(ctx context.Context, tx *ent.Tx, tenantID, actorID int, source string, governance CTIGovernance) error {
	rows, err := tx.SystemConfig.Query().
		Where(systemconfig.TenantIDEQ(tenantID), systemconfig.KeyEQ(CTIGovernanceConfigKey), systemconfig.DeletedAtIsNil()).
		All(ctx)
	if err != nil {
		return err
	}
	if len(rows) > 1 {
		return fmt.Errorf("CTI governance record is duplicated for tenant %d", tenantID)
	}
	wire, err := json.Marshal(ctiGovernanceValue{
		CatalogEnforced:    &governance.CatalogEnforced,
		CompletionEnforced: &governance.CompletionEnforced,
		EffectiveFrom:      formatEffectiveFrom(governance.EffectiveFrom),
	})
	if err != nil {
		return err
	}
	action := "cti_governance.enabled"
	before := "unset"
	switch {
	case len(rows) == 1:
		before = rows[0].Value
		updated, err := tx.SystemConfig.UpdateOneID(rows[0].ID).
			Where(systemconfig.TenantIDEQ(tenantID), systemconfig.DeletedAtIsNil()).
			SetValue(string(wire)).SetValueType("json").SetUpdatedAt(time.Now()).Save(ctx)
		if err != nil {
			return err
		}
		_ = updated
	default:
		created, err := tx.SystemConfig.Create().
			SetKey(CTIGovernanceConfigKey).SetValue(string(wire)).SetValueType("json").
			SetCategory("governance").SetDescription("CTI 分类治理启用记录（受控写入）").
			SetTenantID(tenantID).SetCreatedAt(time.Now()).SetUpdatedAt(time.Now()).Save(ctx)
		if err != nil {
			// 并发首次启用：唯一索引拒绝后明确报冲突，由调用方重试。
			return fmt.Errorf("CTI governance record was created concurrently: %w", err)
		}
		_ = created
	}
	if !governance.CatalogEnforced && !governance.CompletionEnforced {
		action = "cti_governance.paused"
	}
	if _, err := tx.AuditLog.Create().
		SetTenantID(tenantID).SetUserID(actorID).
		SetResource("cti_governance").SetAction(action).
		SetPath("system-configs/governance/cti").SetMethod("PUT").
		SetStatusCode(200).SetResultStatus("applied").
		SetRequestBody(string(wire)).
		SetRequestDigest(ctiGovernanceDigest(before, string(wire))).
		Save(ctx); err != nil {
		return fmt.Errorf("could not record CTI governance audit: %w", err)
	}
	return nil
}

// ctiGovernanceDigest 记录启用记录的前后值指纹，便于审计比对而不泄露额外内容。
func ctiGovernanceDigest(before, after string) string {
	sum := sha256.Sum256([]byte(before + "\x00" + after))
	return hex.EncodeToString(sum[:])
}

func formatEffectiveFrom(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

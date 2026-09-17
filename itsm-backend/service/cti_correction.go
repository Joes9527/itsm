package service

import (
	"context"
	"errors"
	"strings"

	"itsm-backend/common"
	"itsm-backend/ent"
)

// 分类纠正（CTI correction）的共享契约。
//
// 设计边界（2026-08-17 CTI 治理设计 §6）：分类服务拥有树不变量与路径解析，
// **专业服务在自己的事务内调用**这些校验与审计，不集中实现专业状态机。
// 因此这里只提供三件事：
//  1. 目标必须是当前有效的完整三级路径（或显式的“清除”，由调用方决定是否允许）；
//  2. 真正发生变更时必须给出原因；
//  3. 在**同一事务**内落审计（actor/source/原因/前后路径），审计失败整笔回滚。
//
// 逐条纠正的版本冲突、幂等回执与状态机仍由拥有者（专业命令/服务）负责。

// RequireCTICorrectionReason 在分类确实发生变化时要求原因，避免“静默重分类”。
func RequireCTICorrectionReason(reason string, changed bool) error {
	if !changed {
		return nil
	}
	if strings.TrimSpace(reason) == "" {
		return common.NewValidationError("classification correction reason is required", nil)
	}
	return nil
}

// CTICorrectionTargetPolicy 描述纠正目标的完整度要求。
type CTICorrectionTargetPolicy struct {
	// AllowClear 允许把分类改回未分类（保留调用方既有语义）。
	AllowClear bool
	// RequireComplete 要求目标必须是完整三级路径。
	// 目录申请项（Requested Item）必须为 true：目录已声明完整默认分类。
	// 事件/问题/变更等专业纠正为 false：租户可能只维护到二级，
	// 完整度是“完成质量门禁”（B2）在完成时的要求，不是纠正时的门槛，
	// 否则在分类补配完成前工程师无法修正任何记录。
	RequireComplete bool
}

// ValidateCTICorrectionTargetTx 在调用方事务内解析并校验纠正目标。
//
// 目标必须存在、同租户、启用，且父链连续（最多三级）；是否要求“完整三级”
// 由 policy.RequireComplete 决定，见 CTICorrectionTargetPolicy 的说明。
func ValidateCTICorrectionTargetTx(ctx context.Context, tx *ent.Tx, tenantID, targetCategoryID int, policy CTICorrectionTargetPolicy) ([]CTINode, error) {
	if tx == nil {
		return nil, errors.New("classification correction requires the owning transaction")
	}
	if tenantID <= 0 {
		return nil, ErrCTIPathOutsideTenant
	}
	if targetCategoryID <= 0 {
		if policy.AllowClear {
			return nil, nil
		}
		return nil, common.NewValidationError("classification correction requires a classification", nil)
	}
	path, err := NewTicketCategoryService(tx.Client()).ResolveCTIPath(ctx, tx, tenantID, targetCategoryID, policy.RequireComplete, true)
	if err != nil {
		switch {
		case errors.Is(err, ErrCTIPathIncomplete):
			// 只有显式要求完整三级时才会出现：不回退成部分分类。
			return nil, common.NewValidationError("classification correction target must be a complete three-level path", err)
		case errors.Is(err, ErrCTIPathTooDeep), errors.Is(err, ErrCTIPathHierarchy), errors.Is(err, ErrCTIPathInactive):
			return nil, common.NewValidationError("classification correction target is not a usable active path", err)
		case errors.Is(err, ErrCTIPathOutsideTenant), errors.Is(err, ErrCTICategoryNotFound):
			// 跨租户与不存在使用同一错误，避免泄露其它租户对象是否存在。
			return nil, common.NewValidationError("classification correction target is not available in this tenant", err)
		default:
			return nil, err
		}
	}
	return path, nil
}

// CTICorrectionEvidence 记录纠正前后的路径证据。
//
// 证据写进**拥有者既有的操作审计**（如 workitemmutation 的操作回执），
// 而不是新增第二行审计：audit_logs 上有 (tenant_id,user_id,operation_id) 的
// 操作回执唯一索引，同一操作的第二行会直接违反约束。
type CTICorrectionEvidence struct {
	Before []CTINode
	After  []CTINode
}

// Metadata 生成写入既有审计的元数据片段：原因 + 前后完整路径快照。
//
// 快照包含 ID/名称/编码/层级/启用状态，使历史证据不依赖当前分类树状态。
func (e CTICorrectionEvidence) Metadata(reason string) map[string]any {
	return map[string]any{
		"classificationReason": strings.TrimSpace(reason),
		"classificationBefore": ctiPathEvidence(e.Before),
		"classificationAfter":  ctiPathEvidence(e.After),
	}
}

// CTICorrectionBeforePathTx 读取纠正前的路径证据；分类不可解析时返回空（并保留原始 ID 供人排查）。
func CTICorrectionBeforePathTx(ctx context.Context, tx *ent.Tx, tenantID, categoryID int) ([]CTINode, error) {
	if categoryID <= 0 {
		return nil, nil
	}
	path, err := NewTicketCategoryService(tx.Client()).ProjectCTIPath(ctx, tx, tenantID, categoryID)
	if err != nil {
		// 历史脏数据（父级缺失/跨租户）不应阻断纠正本身：调用方仍可改到合法路径，
		// 审计会记录“目标路径”而不伪造旧路径。
		return nil, nil
	}
	return path, nil
}

func ctiPathEvidence(path []CTINode) []map[string]any {
	if len(path) == 0 {
		return nil
	}
	nodes := make([]map[string]any, 0, len(path))
	for _, node := range path {
		nodes = append(nodes, map[string]any{
			"id": node.ID, "name": node.Name, "code": node.Code,
			"level": node.Level, "isActive": node.Active,
		})
	}
	return nodes
}

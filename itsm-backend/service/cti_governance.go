package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/systemconfig"
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
		"resolve": true,
		"close":   true,
		"cancel":  false, // 取消不是完成，沿专业终止命令处理
		"reopen":  false, // 重开后回到处理中，恢复事实由专业命令记录
		"pending": false,
	},
	"incident": {
		"resolve": false, // 恢复服务优先：resolve 不加硬门禁
		"close":   true,  // 关闭需要完整分类
		"reopen":  false,
		"cancel":  false,
	},
	"problem": {
		"close":   true,
		"resolve": false,
		"cancel":  false,
	},
	"change_request": {
		"close":  true,
		"cancel": false,
	},
	"service_request_item": {
		"close":     true,
		"cancel":    false,
		"delivered": false, // 交付确认属于申请侧证据，不在此改写
		"fulfill":   false,
	},
	"catalog_task": {
		// 目录任务不新增完成门禁，保持既有专业语义。
		"close":  false,
		"cancel": false,
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

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

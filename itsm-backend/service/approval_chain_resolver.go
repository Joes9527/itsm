package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"

	"itsm-backend/ent"
	"itsm-backend/ent/approvalchain"
	"itsm-backend/ent/schema"

	"go.uber.org/zap"
)

// ApprovalConfigurationError identifies deterministic policy errors separately
// from database availability failures, including decoding stored chain JSON.
type ApprovalConfigurationError struct{ Cause error }

func (e *ApprovalConfigurationError) Error() string {
	return "invalid approval configuration: " + e.Cause.Error()
}
func (e *ApprovalConfigurationError) Unwrap() error { return e.Cause }

// ResolvedApprovalChain 解析后的审批链，包含实际生效的步骤列表。
type ResolvedApprovalChain struct {
	ChainID int                        `json:"chain_id"`
	Name    string                     `json:"name,omitempty"`
	Steps   []schema.ApprovalChainStep `json:"steps"`
}

// ApprovalChainResolver 解析审批链——根据 SR 的属性（金额、风险等级、组织归属）
// 从租户的 ApprovalChain 配置中选出实际生效的审批步骤。当前实现覆盖金额阈值
// (AmountThreshold) 和集团管控 (GroupControlled)；OrgScope 字段在阶段四
// OrgUnit 模型完成后启用。
type ApprovalChainResolver struct {
	client *ent.Client
	logger *zap.SugaredLogger
}

// NewApprovalChainResolver creates a new ApprovalChainResolver.
func NewApprovalChainResolver(client *ent.Client, logger *zap.SugaredLogger) *ApprovalChainResolver {
	return &ApprovalChainResolver{client: client, logger: logger}
}

// ResolveForServiceRequest 根据服务目录项属性解析审批链。
//
// amount: 服务请求的预估金额（0 表示不涉及金额或未填写）。
// riskLevel: 预留参数，当前不参与计算。
// orgUnitID: 预留参数，阶段四启用。
func (r *ApprovalChainResolver) ResolveForServiceRequest(ctx context.Context, tenantID int,
	amount float64, riskLevel string, orgUnitID int) (*ResolvedApprovalChain, error) {
	return r.resolveServiceRequestCreation(ctx, r.client, tenantID, json.Number(strconv.FormatFloat(amount, 'g', -1, 64)))
}

// ResolveServiceRequestCreation uses the caller transaction and compares the
// submitted decimal directly, without a binary-float request round trip.
func (r *ApprovalChainResolver) ResolveServiceRequestCreation(ctx context.Context, tx *ent.Tx, tenantID int, amount json.Number) (*ResolvedApprovalChain, error) {
	if tx == nil {
		return nil, fmt.Errorf("approval transaction is required")
	}
	return r.resolveServiceRequestCreation(ctx, tx.Client(), tenantID, amount)
}
func (r *ApprovalChainResolver) resolveServiceRequestCreation(ctx context.Context, client *ent.Client, tenantID int, amount json.Number) (*ResolvedApprovalChain, error) {
	if amount == "" {
		amount = "0"
	}
	exact, ok := new(big.Rat).SetString(string(amount))
	if !ok || exact.Sign() < 0 {
		return nil, &ApprovalConfigurationError{Cause: fmt.Errorf("approval amount must be a nonnegative decimal")}
	}
	if client == nil {
		return nil, fmt.Errorf("ent client is nil")
	}

	// 查租户下 entity_type=service_request 的审批链。同一 tenant 下预期只有一条
	// （或按 catalog 分组有多条——当前按 tenant 匹配，多链场景后续扩展）。
	chain, err := client.ApprovalChain.Query().
		Where(
			approvalchain.EntityType("service_request"),
			approvalchain.TenantID(tenantID),
			approvalchain.Status("active"),
		).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// 无配置 = 无审批链，返回空结果而非报错。
			r.logger.Infow("No active approval chain for service_request", "tenant_id", tenantID)
			return nil, nil
		}
		var invalidType *json.UnmarshalTypeError
		var invalidJSON *json.SyntaxError
		if errors.As(err, &invalidType) || errors.As(err, &invalidJSON) {
			return nil, &ApprovalConfigurationError{Cause: err}
		}
		return nil, fmt.Errorf("query approval chain: %w", err)
	}

	// Parse and filter steps
	allSteps, err := parseChainSteps(chain.Chain)
	if err != nil {
		return nil, &ApprovalConfigurationError{Cause: fmt.Errorf("parse chain steps: %w", err)}
	}

	filtered := make([]schema.ApprovalChainStep, 0, len(allSteps))
	for _, step := range allSteps {
		threshold, _ := new(big.Rat).SetString(strconv.FormatFloat(step.AmountThreshold, 'g', -1, 64))
		if !step.GroupControlled && threshold != nil && threshold.Sign() > 0 && exact.Cmp(threshold) < 0 {
			continue
		}
		filtered = append(filtered, step)
	}
	// Sort by level ascending
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Level < filtered[j].Level })

	return &ResolvedApprovalChain{
		ChainID: chain.ID,
		Name:    chain.Name,
		Steps:   filtered,
	}, nil
}

// parseChainSteps 将 ApprovalChain.Chain (any) 转换为 []ApprovalChainStep。
// 支持两种来源：ent 测试环境中直接存入的 []ApprovalChainStep 类型，
// 以及生产环境中 JSON 反序列化的 []interface{} 类型。
func parseChainSteps(raw any) ([]schema.ApprovalChainStep, error) {
	if raw == nil {
		return nil, nil
	}

	// ent 测试环境：SetChain([]ApprovalChainStep{...}) 后读取直接返回该类型。
	if typed, ok := raw.([]schema.ApprovalChainStep); ok {
		return typed, nil
	}

	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("chain is not a JSON array, got %T", raw)
	}
	steps := make([]schema.ApprovalChainStep, 0, len(arr))
	for i, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("chain[%d] is not a JSON object, got %T", i, item)
		}
		var s schema.ApprovalChainStep
		s.Level = intVal(m, "level")
		s.ApproverID = intVal(m, "approver_id")
		s.Role = strVal(m, "role")
		s.Name = strVal(m, "name")
		s.IsRequired = boolVal(m, "is_required")
		s.ApprovalType = strVal(m, "approval_type")
		s.Threshold = intVal(m, "threshold")
		s.OrgScope = strVal(m, "org_scope")
		s.AmountThreshold = floatVal(m, "amount_threshold")
		s.GroupControlled = boolVal(m, "group_controlled")
		steps = append(steps, s)
	}
	return steps, nil
}

func intVal(m map[string]any, key string) int {
	v, _ := m[key].(float64)
	return int(v)
}

func strVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func boolVal(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func floatVal(m map[string]any, key string) float64 {
	v, _ := m[key].(float64)
	return v
}

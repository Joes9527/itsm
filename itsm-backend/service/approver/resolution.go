package approver

import "fmt"

// ResolutionReason 是"为什么选了这个审批人"的封闭词汇表。
//
// 它与引擎的解析分支一一对应：任何未经注册的来源都必须 fail-closed，
// 否则审计里会出现一个没人能解释的取值。
type ResolutionReason string

const (
	// ReasonDirectManager 提单人的直属上级（个人汇报链第一跳）。
	ReasonDirectManager ResolutionReason = "direct_manager"
	// ReasonManagerChainLevel 沿个人汇报链再往上第 N 级。
	ReasonManagerChainLevel ResolutionReason = "manager_chain_level"
	// ReasonDeptManager 所在组织/分公司负责人（组织的轴）。
	ReasonDeptManager ResolutionReason = "dept_manager"
	// ReasonRole 固定岗位/角色。
	ReasonRole ResolutionReason = "role"
	// ReasonExplicitUser 显式指定的人。
	ReasonExplicitUser ResolutionReason = "explicit_user"
	// ReasonCandidateGroup 显式声明的候选组。
	ReasonCandidateGroup ResolutionReason = "candidate_group"
	// ReasonFallbackGroup 兜底组——走到这里说明前面都没解析到人。
	ReasonFallbackGroup ResolutionReason = "fallback_group"
)

var resolutionReasons = map[ResolutionReason]struct{}{
	ReasonDirectManager:     {},
	ReasonManagerChainLevel: {},
	ReasonDeptManager:       {},
	ReasonRole:              {},
	ReasonExplicitUser:      {},
	ReasonCandidateGroup:    {},
	ReasonFallbackGroup:     {},
}

// Resolution 描述一次"谁来做这个审批"的结果及其来源。
//
// 保留 Reason/Level/FallbackUsed 是为了让"为什么是这个人"可审计：
// 只知道审批人是谁，出问题时无法判断是配置错、组织数据错，还是兜底生效。
type Resolution struct {
	ApproverID   int
	Reason       ResolutionReason
	Level        int
	FallbackUsed bool
	Detail       string
}

// Metadata 返回可写入审计的结构化解析来源。
func (r Resolution) Metadata() map[string]any {
	return map[string]any{
		"approverId":   r.ApproverID,
		"reason":       string(r.Reason),
		"level":        r.Level,
		"fallbackUsed": r.FallbackUsed,
		"detail":       r.Detail,
	}
}

// validateResolution 校验解析结果自洽：来源必须在词汇表内、必须有审批人、
// 走到兜底时必须标记为兜底。
func validateResolution(r Resolution) error {
	if _, ok := resolutionReasons[r.Reason]; !ok {
		return fmt.Errorf("unknown resolution reason %q", r.Reason)
	}
	if r.ApproverID == 0 {
		return fmt.Errorf("resolution requires a non-zero approver")
	}
	if r.Reason == ReasonFallbackGroup && !r.FallbackUsed {
		return fmt.Errorf("reason %q must set FallbackUsed", r.Reason)
	}
	return nil
}

package service

import "strings"

// approverFindingMode 是"这个节点用什么方式找人"的一种声明。
//
// 顺序**必须与 createUserTask 的真实优先级一致**：审查中发现，纯 assignee 是在进
// switch 之前就 `assignee := task.Assignee` 取好的（bpmn_process_engine.go:1797），
// 而整个 switch 被 `if assignee == ""` 守卫（:1839）——所以**纯 assignee 优先级最高，
// 高于候选组/候选人**。其余分支按 switch 的先后顺序命中，排在后面的声明会被**静默忽略**。
// 发布校验依赖这个顺序来告诉配置者"哪一个生效、哪一个被丢弃"。
type approverFindingMode string

const (
	modeExplicitAssignee approverFindingMode = "assignee"
	modeCandidate        approverFindingMode = "candidateUsers/candidateGroups"
	modeRole             approverFindingMode = "assigneeRole"
	modeFixedScope       approverFindingMode = "assigneeDeptId/assigneeTeamId/assigneeProjectId/assigneeTempTeamId"
	modeGmChain          approverFindingMode = "assigneeGmChain"
	modeDirectManager    approverFindingMode = "assigneeDirectManager"
	// modeWorkItemAssigneeSource 只走**履约任务**那条分支（非审批），不属于审批 switch
	// 的优先级序列，因此排在最后并单独说明。
	modeWorkItemAssigneeSource approverFindingMode = "assigneeSource"
)

// declaredApproverFindingModes 返回该节点**声明了哪些**找人方式，按引擎真实优先级排列。
//
// 这是"什么算一种找人方式"的**唯一权威**：发布校验与任何后续新增的模式都必须走这里，
// 否则会出现"新加了模式却忘了加进发布校验"的静默缺口——那正是 assigneeDirectManager
// 第一次接入时踩到的坑（只声明它的节点会被发布校验拒绝）。
func declaredApproverFindingModes(t *BPMNUserTask) []approverFindingMode {
	if t == nil {
		return nil
	}
	modes := make([]approverFindingMode, 0, 3)
	// assignee 最先：它在进 switch 之前就被取走，switch 又被 `if assignee == ""` 守卫。
	if strings.TrimSpace(t.Assignee) != "" {
		modes = append(modes, modeExplicitAssignee)
	}
	if strings.TrimSpace(t.CandidateGroups) != "" || strings.TrimSpace(t.CandidateUsers) != "" {
		modes = append(modes, modeCandidate)
	}
	if strings.TrimSpace(t.AssigneeRole) != "" {
		modes = append(modes, modeRole)
	}
	if t.AssigneeDeptId > 0 || t.AssigneeTeamId > 0 || t.AssigneeProjectId > 0 || t.AssigneeTempTeamId > 0 {
		modes = append(modes, modeFixedScope)
	}
	if t.AssigneeGmChain {
		modes = append(modes, modeGmChain)
	}
	if t.AssigneeDirectManager {
		modes = append(modes, modeDirectManager)
	}
	if strings.TrimSpace(t.AssigneeSource) != "" {
		modes = append(modes, modeWorkItemAssigneeSource)
	}
	return modes
}

// approverFindingModeNames 把模式列表转成可读字符串，供错误信息使用。
func approverFindingModeNames(modes []approverFindingMode) []string {
	names := make([]string, 0, len(modes))
	for _, m := range modes {
		names = append(names, string(m))
	}
	return names
}

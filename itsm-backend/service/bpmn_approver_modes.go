package service

import "strings"

// approverFindingMode 是"这个节点用什么方式找人"的一种声明。
//
// 顺序**必须与 createUserTask 的 switch 优先级一致**：引擎的 switch 只会命中
// 第一个成立的分支，排在后面的声明会被**静默忽略**。发布校验依赖这个顺序来告诉
// 配置者"哪一个生效、哪一个被丢掉了"。
type approverFindingMode string

const (
	modeCandidate              approverFindingMode = "candidateUsers/candidateGroups"
	modeRole                   approverFindingMode = "assigneeRole"
	modeFixedScope             approverFindingMode = "assigneeDeptId/assigneeTeamId/assigneeProjectId/assigneeTempTeamId"
	modeGmChain                approverFindingMode = "assigneeGmChain"
	modeDirectManager          approverFindingMode = "assigneeDirectManager"
	modeExplicitAssignee       approverFindingMode = "assignee"
	modeWorkItemAssigneeSource approverFindingMode = "assigneeSource"
)

// declaredApproverFindingModes 返回该节点**声明了哪些**找人方式，按引擎优先级排列。
//
// 这是"什么算一种找人方式"的**唯一权威**：发布校验与任何后续新增的模式都必须走这里，
// 否则会出现"新加了模式却忘了加进发布校验"的静默缺口——那正是 assigneeDirectManager
// 第一次接入时踩到的坑（只声明它的节点会被发布校验拒绝）。
func declaredApproverFindingModes(t *BPMNUserTask) []approverFindingMode {
	if t == nil {
		return nil
	}
	modes := make([]approverFindingMode, 0, 3)
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
	if strings.TrimSpace(t.Assignee) != "" {
		modes = append(modes, modeExplicitAssignee)
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

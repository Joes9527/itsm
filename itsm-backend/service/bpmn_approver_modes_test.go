package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeclaredApproverFindingModesIsEmptyWhenNothingIsDeclared(t *testing.T) {
	require.Empty(t, declaredApproverFindingModes(&BPMNUserTask{ID: "T"}))
	require.Empty(t, declaredApproverFindingModes(nil))
}

func TestDeclaredApproverFindingModesRecognisesEachMode(t *testing.T) {
	cases := []struct {
		name string
		task *BPMNUserTask
		want approverFindingMode
	}{
		{"candidateUsers", &BPMNUserTask{CandidateUsers: "42"}, modeCandidate},
		{"candidateGroups", &BPMNUserTask{CandidateGroups: "ticket-approvers"}, modeCandidate},
		{"assigneeRole", &BPMNUserTask{AssigneeRole: "network_eng"}, modeRole},
		{"assigneeDeptId", &BPMNUserTask{AssigneeDeptId: 7}, modeFixedScope},
		{"assigneeTeamId", &BPMNUserTask{AssigneeTeamId: 7}, modeFixedScope},
		{"assigneeProjectId", &BPMNUserTask{AssigneeProjectId: 7}, modeFixedScope},
		{"assigneeTempTeamId", &BPMNUserTask{AssigneeTempTeamId: 7}, modeFixedScope},
		{"assigneeGmChain", &BPMNUserTask{AssigneeGmChain: true}, modeGmChain},
		{"assigneeDirectManager", &BPMNUserTask{AssigneeDirectManager: true}, modeDirectManager},
		{"assignee", &BPMNUserTask{Assignee: "42"}, modeExplicitAssignee},
		{"assigneeSource", &BPMNUserTask{AssigneeSource: "work_item_assignee"}, modeWorkItemAssigneeSource},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := declaredApproverFindingModes(c.task)
			require.Len(t, got, 1)
			require.Equal(t, c.want, got[0])
		})
	}
}

// candidateUsers 与 candidateGroups 是**同一种**方式（都靠候选展开），
// 同时声明两者不得被当成"声明了两种方式"而误拒。
func TestCandidateUsersAndGroupsCountAsOneMode(t *testing.T) {
	got := declaredApproverFindingModes(&BPMNUserTask{
		CandidateUsers:  "42",
		CandidateGroups: "ticket-approvers",
	})
	require.Len(t, got, 1)
	require.Equal(t, modeCandidate, got[0])
}

// 多个固定范围同样只是一种方式（引擎按声明顺序逐个尝试）。
func TestMultipleFixedScopesCountAsOneMode(t *testing.T) {
	got := declaredApproverFindingModes(&BPMNUserTask{AssigneeDeptId: 1, AssigneeTeamId: 2, AssigneeProjectId: 3})
	require.Len(t, got, 1)
	require.Equal(t, modeFixedScope, got[0])
}

// 顺序必须与 createUserTask 的 switch 优先级一致：错误信息要指出"哪个生效、哪些被丢弃"，
// 顺序错了就会指错人。
func TestDeclaredApproverFindingModesFollowsEnginePrecedence(t *testing.T) {
	got := declaredApproverFindingModes(&BPMNUserTask{
		CandidateUsers:        "42",
		AssigneeRole:          "network_eng",
		AssigneeTeamId:        7,
		AssigneeGmChain:       true,
		AssigneeDirectManager: true,
		Assignee:              "99",
		AssigneeSource:        "work_item_assignee",
	})
	// assignee 必须排第一：引擎在进 switch 之前就 `assignee := task.Assignee`，
	// 而 switch 被 `if assignee == ""` 守卫（bpmn_process_engine.go:1797/1839）。
	// 早先把 assignee 排在最后是错的——错误信息会指错"生效的那个"。
	require.Equal(t, []approverFindingMode{
		modeExplicitAssignee,
		modeCandidate,
		modeRole,
		modeFixedScope,
		modeGmChain,
		modeDirectManager,
		modeWorkItemAssigneeSource,
	}, got, "顺序必须与引擎真实优先级一致（assignee 最高，因为它跳过整个 switch）")
}

// 纯 assignee 与候选组并存时，引擎实际生效的是 assignee——发布校验的错误信息
// 必须说对是哪一方生效，否则操作者会去改错字段。
func TestExplicitAssigneeOutranksCandidates(t *testing.T) {
	got := declaredApproverFindingModes(&BPMNUserTask{Assignee: "99", CandidateGroups: "support"})
	require.Equal(t, modeExplicitAssignee, got[0],
		"assignee 跳过整个 switch，因此它才是生效的那个")
}

func TestApproverFindingModeNamesAreReadable(t *testing.T) {
	names := approverFindingModeNames([]approverFindingMode{modeCandidate, modeRole})
	require.Equal(t, []string{"candidateUsers/candidateGroups", "assigneeRole"}, names)
}

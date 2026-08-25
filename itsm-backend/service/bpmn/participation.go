package bpmn

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/user"
)

// CallerIdentity captures every string form a caller can be matched against
// on a ProcessTask's Assignee/CandidateUsers/CandidateGroups CSV columns:
// numeric user ID, username, or email — Assignee/CandidateUsers can hold any
// of the three depending on which code path wrote them (designer-set
// assignees are usernames, auto-resolved assignees are numeric IDs) — plus
// the caller's resolved group/role names for CandidateGroups matching.
type CallerIdentity struct {
	IDStr     string
	Username  string
	Email     string
	GroupsCSV string
}

// ResolveCallerIdentity loads the identity forms needed to evaluate task
// participation for userID. This is the SINGLE place that resolves "who is
// this caller, for BPMN candidate-matching purposes" — authorizeTaskActor,
// isTaskCandidate, ListUserTasks, GetTask, and ListProcessInstances must all
// call this instead of independently re-deriving these fields, so the
// matching rules cannot silently drift apart across call sites.
func ResolveCallerIdentity(ctx context.Context, client *ent.Client, groupResolver *GroupResolver, tenantID, userID int) (*CallerIdentity, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("无效的用户ID")
	}
	actor, err := client.User.Query().Where(user.ID(userID), user.TenantID(tenantID)).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("用户不存在: %w", err)
	}
	identity := &CallerIdentity{
		IDStr:    strconv.Itoa(userID),
		Username: strings.TrimSpace(actor.Username),
		Email:    strings.TrimSpace(actor.Email),
	}
	if groupResolver != nil {
		groupsCSV, gErr := groupResolver.GetUserGroupNames(ctx, tenantID, userID)
		if gErr != nil {
			return nil, fmt.Errorf("查询用户所属组失败: %w", gErr)
		}
		identity.GroupsCSV = groupsCSV
	}
	return identity, nil
}

// IsTaskParticipant reports whether this identity is the task's assignee, is
// listed in its candidate_users, or belongs to a role/group listed in its
// candidate_groups. assignee/candidate_users are matched by exact token
// after splitting on commas (a task's CSV may hold IDs, usernames, or
// emails in any position). candidate_groups is matched by exact token on
// both sides — NOT the substring-of-whole-CSV comparison ListUserTasks's
// query used before Task 3 of this plan, which only worked reliably for
// single-group callers.
func (id *CallerIdentity) IsTaskParticipant(task *ent.ProcessTask) bool {
	matchesUser := func(csv string) bool {
		for _, candidate := range strings.Split(csv, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			if candidate == id.IDStr || candidate == id.Username || (id.Email != "" && candidate == id.Email) {
				return true
			}
		}
		return false
	}
	if matchesUser(task.Assignee) || matchesUser(task.CandidateUsers) {
		return true
	}
	if id.GroupsCSV == "" || task.CandidateGroups == "" {
		return false
	}
	callerGroups := make(map[string]bool)
	for _, g := range strings.Split(id.GroupsCSV, ",") {
		g = strings.TrimSpace(g)
		if g != "" {
			callerGroups[g] = true
		}
	}
	for _, g := range strings.Split(task.CandidateGroups, ",") {
		g = strings.TrimSpace(g)
		if g != "" && callerGroups[g] {
			return true
		}
	}
	return false
}

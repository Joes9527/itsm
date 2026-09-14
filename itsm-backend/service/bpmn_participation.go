package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	entsql "entgo.io/ent/dialect/sql"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/group"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/role"
	"itsm-backend/ent/user"
	"itsm-backend/service/bpmn"
)

type bpmnActorIdentity struct {
	UserID, TenantID int
	UserTokens       map[string]struct{}
	GroupTokens      map[string]struct{}
}

type bpmnParticipationResolver struct {
	client        *ent.Client
	groupResolver *bpmn.GroupResolver
	directory     database.DirectorySnapshot
	owningTx      *ent.Tx
	// Populated only on a short-lived read projection resolver, never on the engine's mutation resolver.
	readActor *bpmnActorIdentity
}

func newBPMNParticipationResolver(client *ent.Client, groupResolver *bpmn.GroupResolver) *bpmnParticipationResolver {
	return &bpmnParticipationResolver{client: client, groupResolver: groupResolver}
}

func (r *bpmnParticipationResolver) forClient(client *ent.Client) *bpmnParticipationResolver {
	if client == nil || (client == r.client && r.readActor != nil) {
		return r
	}
	clone := *r
	clone.client = client
	clone.readActor = nil
	clone.groupResolver = bpmn.NewGroupResolver(client)
	return &clone
}

func (r *bpmnParticipationResolver) resolveActor(ctx context.Context, scope BPMNAccessScope) (*bpmnActorIdentity, error) {
	if r.readActor != nil && r.readActor.UserID == scope.UserID && r.readActor.TenantID == scope.TenantID {
		return r.readActor, nil
	}
	if r.directory != nil {
		if r.owningTx == nil {
			tx, err := r.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
			if err != nil {
				return nil, err
			}
			defer tx.Rollback()
			clone := r.forClient(tx.Client())
			clone.owningTx = tx
			return clone.resolveActor(ctx, scope)
		}
		actor, err := authorization.ResolveLifecycleActor(ctx, r.owningTx, r.directory, scope.UserID, scope.TenantID)
		if err != nil {
			return nil, err
		}
		identity := &bpmnActorIdentity{UserID: actor.ID, TenantID: scope.TenantID, UserTokens: map[string]struct{}{}, GroupTokens: map[string]struct{}{}}
		addToken(identity.UserTokens, strconv.Itoa(actor.ID))
		addToken(identity.UserTokens, actor.Username)
		addToken(identity.UserTokens, actor.Email)
		addToken(identity.GroupTokens, authorization.EffectiveSessionRole(actor))
		roles, err := r.client.Role.Query().Where(role.TenantID(scope.TenantID), role.IsActive(true), func(s *entsql.Selector) {
			edge := entsql.Table(role.UsersTable)
			s.Where(entsql.In(s.C(role.FieldID), entsql.Select(edge.C("role_id")).From(edge).Where(entsql.EQ(edge.C("user_id"), actor.ID))))
		}).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, assignedRole := range roles {
			addToken(identity.GroupTokens, assignedRole.Code)
		}
		// Group.Members is users.group_members; User.Groups is a distinct
		// edge and cannot establish this task's configured membership.
		directory, closeDirectory, err := r.directory.Open(ctx, r.owningTx, scope.TenantID)
		if err != nil || directory == nil || closeDirectory == nil {
			if closeDirectory != nil {
				err = errors.Join(err, closeDirectory())
			}
			return nil, errors.Join(errors.New("BPMN group directory unavailable"), err)
		}
		var memberships []struct {
			GroupID *int `json:"group_members"`
		}
		err = directory.User.Query().Where(user.ID(actor.ID)).Select(group.MembersColumn).Scan(tenantctx.SystemContext(ctx, "bpmn:group-membership", "read authorized actor membership at owning snapshot"), &memberships)
		closeErr := closeDirectory()
		if err != nil || closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		if len(memberships) != 1 {
			return nil, fmt.Errorf("BPMN actor membership unavailable")
		}
		if memberships[0].GroupID != nil {
			groups, err := r.client.Group.Query().Where(group.ID(*memberships[0].GroupID), group.TenantID(scope.TenantID)).All(ctx)
			if err != nil {
				return nil, err
			}
			for _, memberGroup := range groups {
				addToken(identity.GroupTokens, memberGroup.Name)
			}
		}
		return identity, nil
	}
	actor, err := r.client.User.Query().
		Where(
			user.ID(scope.UserID),
			user.TenantID(scope.TenantID),
			user.Active(true),
		).
		WithRoles(func(query *ent.RoleQuery) {
			query.Where(role.TenantID(scope.TenantID))
		}).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve BPMN actor: %w", err)
	}

	identity := &bpmnActorIdentity{
		UserID:      actor.ID,
		TenantID:    actor.TenantID,
		UserTokens:  make(map[string]struct{}),
		GroupTokens: make(map[string]struct{}),
	}
	addToken(identity.UserTokens, strconv.Itoa(actor.ID))
	addToken(identity.UserTokens, actor.Username)
	addToken(identity.UserTokens, actor.Email)
	addToken(identity.GroupTokens, actor.Role)
	for _, additionalRole := range actor.Edges.Roles {
		addToken(identity.GroupTokens, additionalRole.Code)
	}

	if r.groupResolver != nil {
		groupNames, groupErr := r.groupResolver.GetUserGroupNames(ctx, scope.TenantID, actor.ID)
		if groupErr != nil {
			return nil, fmt.Errorf("resolve BPMN actor groups: %w", groupErr)
		}
		for _, groupName := range csvTokens(groupNames) {
			identity.GroupTokens[groupName] = struct{}{}
		}
	}

	return identity, nil
}

func (r *bpmnParticipationResolver) matchesTask(task *ent.ProcessTask, actor *bpmnActorIdentity) bool {
	if task == nil || actor == nil || task.TenantID != actor.TenantID {
		return false
	}
	return containsToken(task.Assignee, actor.UserTokens) ||
		containsToken(task.CandidateUsers, actor.UserTokens) ||
		containsToken(task.CandidateGroups, actor.GroupTokens)
}

func (r *bpmnParticipationResolver) participatingInstanceIDs(ctx context.Context, actor *bpmnActorIdentity) ([]int, error) {
	if actor == nil || actor.UserID <= 0 || actor.TenantID <= 0 {
		return nil, fmt.Errorf("invalid BPMN actor identity")
	}

	prefilter := make([]predicate.ProcessTask, 0, len(actor.UserTokens)*2+len(actor.GroupTokens))
	for token := range actor.UserTokens {
		prefilter = append(prefilter,
			processtask.AssigneeContainsFold(token),
			processtask.CandidateUsersContainsFold(token),
		)
	}
	for token := range actor.GroupTokens {
		prefilter = append(prefilter, processtask.CandidateGroupsContainsFold(token))
	}

	tasks, err := r.client.ProcessTask.Query().
		Where(
			processtask.TenantID(actor.TenantID),
			processtask.Or(prefilter...),
		).
		Order(ent.Asc(processtask.FieldID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query BPMN participant tasks: %w", err)
	}

	seen := make(map[int]struct{}, len(tasks))
	instanceIDs := make([]int, 0, len(tasks))
	for _, task := range tasks {
		if !r.matchesTask(task, actor) {
			continue
		}
		if _, exists := seen[task.ProcessInstanceID]; exists {
			continue
		}
		seen[task.ProcessInstanceID] = struct{}{}
		instanceIDs = append(instanceIDs, task.ProcessInstanceID)
	}
	if len(instanceIDs) == 0 {
		return nil, nil
	}

	tenantInstanceIDs, err := r.client.ProcessInstance.Query().
		Where(
			processinstance.TenantID(actor.TenantID),
			processinstance.IDIn(instanceIDs...),
		).
		Select(processinstance.FieldID).
		Ints(ctx)
	if err != nil {
		return nil, fmt.Errorf("validate BPMN participant instances: %w", err)
	}

	allowed := make(map[int]struct{}, len(tenantInstanceIDs))
	for _, instanceID := range tenantInstanceIDs {
		allowed[instanceID] = struct{}{}
	}
	validated := make([]int, 0, len(tenantInstanceIDs))
	for _, instanceID := range instanceIDs {
		if _, ok := allowed[instanceID]; ok {
			validated = append(validated, instanceID)
		}
	}
	return validated, nil
}

func csvTokens(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if token := strings.ToLower(strings.TrimSpace(part)); token != "" {
			out = append(out, token)
		}
	}
	return out
}

func addToken(tokens map[string]struct{}, value string) {
	for _, token := range csvTokens(value) {
		tokens[token] = struct{}{}
	}
}

func containsToken(value string, allowed map[string]struct{}) bool {
	for _, token := range csvTokens(value) {
		if _, ok := allowed[token]; ok {
			return true
		}
	}
	return false
}

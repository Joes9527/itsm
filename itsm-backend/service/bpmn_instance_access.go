package service

import (
	"context"
	"fmt"
	"strconv"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/processinstance"
)

type bpmnInstanceAccessPolicy struct {
	client                *ent.Client
	participationResolver *bpmnParticipationResolver
}

func newBPMNInstanceAccessPolicy(client *ent.Client, participationResolver *bpmnParticipationResolver) *bpmnInstanceAccessPolicy {
	return &bpmnInstanceAccessPolicy{
		client:                client,
		participationResolver: participationResolver,
	}
}

func (p *bpmnInstanceAccessPolicy) forClient(client *ent.Client) *bpmnInstanceAccessPolicy {
	if client == nil {
		return p
	}
	var resolver *bpmnParticipationResolver
	if p != nil && p.participationResolver != nil {
		resolver = p.participationResolver.forClient(client)
	}
	return newBPMNInstanceAccessPolicy(client, resolver)
}

func (p *bpmnInstanceAccessPolicy) loadTenantScoped(ctx context.Context, instanceKey string, tenantID int) (*ent.ProcessInstance, error) {
	if p == nil || p.client == nil || tenantID <= 0 {
		return nil, common.NewForbiddenError("缺少 BPMN 实例租户上下文")
	}
	var instancePredicate predicate.ProcessInstance
	if id, err := strconv.Atoi(instanceKey); err == nil {
		instancePredicate = processinstance.ID(id)
	} else {
		instancePredicate = processinstance.ProcessInstanceID(instanceKey)
	}
	instance, err := p.client.ProcessInstance.Query().
		Where(
			processinstance.TenantID(tenantID),
			instancePredicate,
		).
		First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程实例失败: %w", err)
	}
	return instance, nil
}

func (p *bpmnInstanceAccessPolicy) loadForRead(ctx context.Context, instanceKey string) (*ent.ProcessInstance, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	instance, err := p.loadTenantScoped(ctx, instanceKey, scope.TenantID)
	if err != nil {
		return nil, err
	}
	if scope.CanReadAllInstances || instance.Initiator == strconv.Itoa(scope.UserID) {
		return instance, nil
	}
	if p.participationResolver == nil {
		return nil, common.NewForbiddenError("无权读取流程实例")
	}
	actor, err := p.participationResolver.resolveActor(ctx, scope)
	if err != nil {
		return nil, common.NewForbiddenError("无权读取流程实例")
	}
	instanceIDs, err := p.participationResolver.participatingInstanceIDs(ctx, actor)
	if err != nil {
		return nil, fmt.Errorf("解析流程实例参与范围失败: %w", err)
	}
	for _, instanceID := range instanceIDs {
		if instanceID == instance.ID {
			return instance, nil
		}
	}
	return nil, common.NewForbiddenError("无权读取流程实例")
}

func (p *bpmnInstanceAccessPolicy) loadForUpdate(ctx context.Context, instanceKey string) (*ent.ProcessInstance, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if !scope.CanUpdateAllInstances {
		return nil, common.NewForbiddenError("无权修改流程实例")
	}
	return p.loadTenantScoped(ctx, instanceKey, scope.TenantID)
}

func (p *bpmnInstanceAccessPolicy) authorizedInstanceIDs(ctx context.Context) ([]int, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if p == nil || p.client == nil {
		return nil, common.NewForbiddenError("缺少 BPMN 实例租户上下文")
	}
	if scope.CanReadAllInstances {
		ids, err := p.client.ProcessInstance.Query().
			Where(processinstance.TenantID(scope.TenantID)).
			Select(processinstance.FieldID).
			Ints(ctx)
		if err != nil {
			return nil, fmt.Errorf("获取流程实例授权范围失败: %w", err)
		}
		return ids, nil
	}
	if p.participationResolver == nil {
		return nil, common.NewForbiddenError("无权读取流程实例")
	}
	actor, err := p.participationResolver.resolveActor(ctx, scope)
	if err != nil {
		return nil, common.NewForbiddenError("无权读取流程实例")
	}
	participatingIDs, err := p.participationResolver.participatingInstanceIDs(ctx, actor)
	if err != nil {
		return nil, fmt.Errorf("解析流程实例参与范围失败: %w", err)
	}
	initiatedIDs, err := p.client.ProcessInstance.Query().
		Where(
			processinstance.TenantID(scope.TenantID),
			processinstance.Initiator(strconv.Itoa(scope.UserID)),
		).
		Select(processinstance.FieldID).
		Ints(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程实例发起范围失败: %w", err)
	}

	seen := make(map[int]struct{}, len(initiatedIDs)+len(participatingIDs))
	ids := make([]int, 0, len(initiatedIDs)+len(participatingIDs))
	for _, instanceID := range initiatedIDs {
		seen[instanceID] = struct{}{}
		ids = append(ids, instanceID)
	}
	for _, instanceID := range participatingIDs {
		if _, ok := seen[instanceID]; ok {
			continue
		}
		seen[instanceID] = struct{}{}
		ids = append(ids, instanceID)
	}
	return ids, nil
}

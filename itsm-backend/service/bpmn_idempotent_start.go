package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processinstance"
)

type ProcessDefinitionIdentity struct {
	ID      int
	Key     string
	Version string
	Digest  string
}

// FreezeProcessDefinition captures the exact XML read by the owning resolver.
func FreezeProcessDefinition(definition *ent.ProcessDefinition) ProcessDefinitionIdentity {
	return ProcessDefinitionIdentity{ID: definition.ID, Key: definition.Key, Version: definition.Version, Digest: fmt.Sprintf("%x", sha256.Sum256(definition.BpmnXML))}
}

type processStartConflictError struct{}

func (*processStartConflictError) Error() string {
	return "process start identity or context conflicts with committed instance"
}

type processStartDefinitionError struct{}

func (*processStartDefinitionError) Error() string {
	return "frozen process definition unavailable or changed"
}

// StartProcessByDefinitionID starts the frozen definition chosen by Intake.
// The existing unique BPMN instance identity is the durable deduplication
// boundary, including completed instances and crashes before outbox acknowledgement.
// A concurrent loser rolls back its entire attempt before reading the winner.
func (e *CustomProcessEngine) StartProcessByDefinitionID(ctx context.Context, definition ProcessDefinitionIdentity, businessKey, businessType string, businessID int, variables map[string]interface{}, startKey string) (*ent.ProcessInstance, error) {
	if e.transactionBound {
		return nil, fmt.Errorf("idempotent process start must own its transaction")
	}
	tenantID, err := bpmnAuthorizedTenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if definition.ID <= 0 || definition.Key == "" || definition.Version == "" || len(definition.Digest) != 64 || businessID <= 0 || strings.TrimSpace(startKey) == "" || strings.TrimSpace(businessKey) == "" || strings.TrimSpace(businessType) == "" {
		return nil, fmt.Errorf("complete process start identity is required")
	}
	if actor, ok := ctx.Value(intakeStartActorKey{}).(intakeStartActor); ok && (actor.targetTenantID != tenantID || actor.workItemID != businessID) {
		return nil, fmt.Errorf("intake start business identity mismatch")
	}
	encoded, err := json.Marshal(struct {
		Definition                           ProcessDefinitionIdentity
		TenantID                             int
		Initiator, BusinessKey, BusinessType string
		BusinessID                           int
		Variables                            map[string]interface{}
	}{definition, tenantID, resolveProcessInitiator(ctx, variables), businessKey, businessType, businessID, variables})
	if err != nil {
		return nil, fmt.Errorf("invalid process start context: %w", err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(encoded))
	// The engine may advance its variable map. Preserve the caller's confirmed input.
	if variables != nil {
		raw, err := json.Marshal(variables)
		if err != nil {
			return nil, err
		}
		clone := json.NewDecoder(bytes.NewReader(raw))
		clone.UseNumber()
		var copied map[string]interface{}
		if err = clone.Decode(&copied); err != nil {
			return nil, err
		}
		variables = copied
	}
	identity := fmt.Sprintf("PI-start-%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%s", tenantID, startKey))))
	load := func() (*ent.ProcessInstance, error) {
		tx, err := e.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if _, _, err := e.forClient(tx.Client(), nil, tx).resolveBPMNProcessStartActor(ctx, tx.Client(), tenantID, variables); err != nil {
			return nil, err
		}
		existing, err := tx.ProcessInstance.Query().Where(processinstance.ProcessInstanceID(identity), processinstance.TenantID(tenantID)).Only(ctx)
		if err != nil {
			return nil, err
		}
		if existing.StartRequestDigest == "" || existing.StartRequestDigest != digest || existing.ProcessDefinitionID != definition.ID || existing.BusinessKey != businessKey || existing.BusinessType != businessType || existing.BusinessID != businessID || existing.Initiator != resolveProcessInitiator(ctx, variables) {
			return nil, &processStartConflictError{}
		}
		if err := validateGenericWorkflowReplay(ctx, tx.Client(), definition, existing, variables); err != nil {
			return nil, err
		}
		existing.Unwrap()
		return existing, nil
	}
	if existing, err := load(); !ent.IsNotFound(err) {
		return existing, err
	}
	tx, err := e.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	resolved, err := tx.ProcessDefinition.Query().Where(processdefinition.ID(definition.ID), processdefinition.Key(definition.Key), processdefinition.Version(definition.Version), processdefinition.TenantID(tenantID), processdefinition.IsActive(true)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, &processStartDefinitionError{}
		}
		return nil, fmt.Errorf("query frozen process definition: %w", err)
	}
	if FreezeProcessDefinition(resolved).Digest != definition.Digest {
		return nil, &processStartDefinitionError{}
	}
	keys := make([]string, 0)
	txEngine := e.forClient(tx.Client(), &keys, tx)
	instance, err := txEngine.startResolvedProcess(ctx, resolved, businessKey, businessType, businessID, variables, identity, digest)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsConstraintError(err) {
			if existing, replayErr := load(); !ent.IsNotFound(replayErr) {
				return existing, replayErr
			}
		}
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit process start: %w", err)
	}
	instance.Unwrap()
	e.processCommittedCallbackKeys(ctx, tenantID, keys)
	return instance, nil
}

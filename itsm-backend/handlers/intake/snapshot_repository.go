package intake

import (
	"context"
	"encoding/json"
	"strings"

	"itsm-backend/ent"
)

type SnapshotInput struct {
	TenantID                  int
	IntakeRequestID           int
	WorkItemID                int
	Channel                   string
	SourceProvider            string
	SourceEventID             string
	SourceConversationID      string
	CatalogItemID             *int
	CatalogVersion            string
	RecordClass               string
	CTISnapshot               map[string]any
	CIIDs                     []int
	FormSchemaVersion         string
	WorkflowDefinitionID      *int
	WorkflowDefinitionKey     string
	WorkflowDefinitionVersion string
	NoProcess                 bool
	SLADefinitionID           *int
	ResolverVersion           string
	RequestDigest             string
}

type SnapshotRepository struct{}

func NewSnapshotRepository() *SnapshotRepository {
	return &SnapshotRepository{}
}

func (r *SnapshotRepository) Create(ctx context.Context, tx *ent.Tx, input SnapshotInput) (*ent.IntakeResolutionSnapshot, error) {
	if tx == nil {
		return nil, NewInternalFailure("intake transaction is required", nil)
	}
	if input.TenantID <= 0 || input.IntakeRequestID <= 0 || input.WorkItemID <= 0 || input.Channel == "" || input.RecordClass == "" || input.ResolverVersion == "" || input.RequestDigest == "" {
		return nil, NewInvalidCommand("invalid intake resolution snapshot", FieldError{Field: "snapshot", Message: "required resolution evidence is missing"}, nil)
	}
	if !input.NoProcess && input.WorkflowDefinitionID == nil {
		return nil, NewWorkflowBindingRequired("a frozen workflow binding or explicit no-process decision is required", nil)
	}
	if input.NoProcess && input.WorkflowDefinitionID != nil {
		return nil, NewInvalidCommand("invalid intake resolution snapshot", FieldError{Field: "workflowDefinitionId", Message: "must be absent when noProcess is true"}, nil)
	}
	if input.WorkflowDefinitionID != nil && (input.WorkflowDefinitionKey == "" || input.WorkflowDefinitionVersion == "") {
		return nil, NewWorkflowBindingRequired("workflow definition key and version are required", nil)
	}
	if key := forbiddenSnapshotKey(input.CTISnapshot); key != "" {
		return nil, NewInvalidCommand("invalid intake resolution snapshot", FieldError{Field: "ctiSnapshot." + key, Message: "authoritative or sensitive data is not allowed"}, nil)
	}
	cti, err := json.Marshal(input.CTISnapshot)
	if err != nil {
		return nil, NewInvalidCommand("invalid intake resolution snapshot", FieldError{Field: "ctiSnapshot", Message: "must contain JSON-compatible evidence"}, err)
	}

	create := tx.IntakeResolutionSnapshot.Create().
		SetTenantID(input.TenantID).
		SetIntakeRequestID(input.IntakeRequestID).
		SetWorkItemID(input.WorkItemID).
		SetChannel(input.Channel).
		SetSourceProvider(input.SourceProvider).
		SetRecordClass(input.RecordClass).
		SetCiIds(append([]int(nil), input.CIIDs...)).
		SetNoProcess(input.NoProcess).
		SetResolverVersion(input.ResolverVersion).
		SetRequestDigest(input.RequestDigest)
	if len(input.CTISnapshot) > 0 {
		create.SetCtiSnapshot(cti)
	}
	if input.SourceEventID != "" {
		create.SetSourceEventID(input.SourceEventID)
	}
	if input.SourceConversationID != "" {
		create.SetSourceConversationID(input.SourceConversationID)
	}
	if input.CatalogItemID != nil {
		create.SetCatalogItemID(*input.CatalogItemID)
	}
	if input.CatalogVersion != "" {
		create.SetCatalogVersion(input.CatalogVersion)
	}
	if input.FormSchemaVersion != "" {
		create.SetFormSchemaVersion(input.FormSchemaVersion)
	}
	if input.WorkflowDefinitionID != nil {
		create.SetWorkflowDefinitionID(*input.WorkflowDefinitionID)
	}
	if input.WorkflowDefinitionKey != "" {
		create.SetWorkflowDefinitionKey(input.WorkflowDefinitionKey)
	}
	if input.WorkflowDefinitionVersion != "" {
		create.SetWorkflowDefinitionVersion(input.WorkflowDefinitionVersion)
	}
	if input.SLADefinitionID != nil {
		create.SetSLADefinitionID(*input.SLADefinitionID)
	}

	created, err := create.Save(ctx)
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not create intake resolution snapshot", err)
	}
	return created, nil
}

func forbiddenSnapshotKey(value any) string {
	forbidden := map[string]struct{}{
		"title": {}, "description": {}, "requester": {}, "formvalues": {},
		"token": {}, "secret": {}, "authorization": {}, "password": {},
	}
	var visit func(any) string
	visit = func(current any) string {
		switch typed := current.(type) {
		case map[string]any:
			for key, nested := range typed {
				if _, blocked := forbidden[strings.ToLower(strings.TrimSpace(key))]; blocked {
					return key
				}
				if found := visit(nested); found != "" {
					return found
				}
			}
		case []any:
			for _, nested := range typed {
				if found := visit(nested); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return visit(value)
}

package service_request

import (
	"context"
	"fmt"
	"itsm-backend/ent"
	"itsm-backend/ent/externalidentity"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/servicerequestaccesssnapshot"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	"itsm-backend/handlers/common/accessgrant"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/service"
)

func prepareAccessSnapshot(ctx context.Context, tx *ent.Tx, in creation.ResolvedIntake) (*accessgrant.ApprovalSnapshot, error) {
	p := in.Catalog.AccessPolicy
	if p == nil {
		return nil, nil
	}
	fields := make([]service.FieldDefinitionInput, 0, len(in.FieldDefinitions))
	for _, f := range in.FieldDefinitions {
		fields = append(fields, service.FieldDefinitionInput{Name: f.Key, FieldType: f.DataType, Required: f.Required, Options: f.Options})
	}
	if err := service_catalog.ValidateAccessPolicy(p, fields); err != nil {
		return nil, creation.NewDomainValidationFailed("invalid finite access policy", err)
	}
	key, ok := in.Command.FormValues[p.DurationField].(string)
	if !ok {
		return nil, creation.NewDomainValidationFailed("a configured access duration is required", nil)
	}
	var seconds int64
	for _, o := range p.DurationOptions {
		if o.Key == key {
			seconds = o.Seconds
			break
		}
	}
	if seconds <= 0 {
		return nil, creation.NewDomainValidationFailed("unknown access duration", nil)
	}
	// Exact target provider/workspace mapping belongs to the authenticated requester,
	// not an applicant form field or the inbound KAF assertion's provider subject.
	mapping, err := tx.ExternalIdentity.Query().Where(externalidentity.TenantIDEQ(in.Identity.TenantID), externalidentity.ProviderEQ(string(p.Provider)), externalidentity.WorkspaceEQ(p.ExternalSystem), externalidentity.UserIDEQ(in.Identity.RequesterID), externalidentity.ActiveEQ(true), externalidentity.HasUserWith(user.TenantIDEQ(in.Identity.TenantID), user.ActiveEQ(true))).Only(ctx)
	if err != nil {
		return nil, creation.NewDomainValidationFailed("trusted external requester identity is unavailable or ambiguous", err)
	}
	return &accessgrant.ApprovalSnapshot{PolicyID: p.ID, PolicyVersion: p.Version, Provider: p.Provider, ExternalSystem: p.ExternalSystem, SubjectID: mapping.Subject, GroupID: p.GroupID, DurationKey: key, DurationSeconds: seconds}, nil
}
func saveAccessSnapshot(ctx context.Context, tx *ent.Tx, itemID int, p *accessgrant.ApprovalSnapshot) error {
	if p == nil {
		return nil
	}
	_, err := tx.ServiceRequestAccessSnapshot.Create().SetWorkItemID(itemID).SetPolicyID(p.PolicyID).SetPolicyVersion(p.PolicyVersion).SetProvider(servicerequestaccesssnapshot.Provider(p.Provider)).SetExternalSystem(p.ExternalSystem).SetSubjectID(p.SubjectID).SetGroupID(p.GroupID).SetDurationKey(p.DurationKey).SetDurationSeconds(p.DurationSeconds).Save(ctx)
	return err
}
func (s *Service) ReadAccessSnapshot(ctx context.Context, client *ent.Client, tenantID, itemID int) (*accessgrant.ApprovalSnapshot, error) {
	row, err := client.ServiceRequestAccessSnapshot.Query().Where(servicerequestaccesssnapshot.WorkItemIDEQ(itemID), servicerequestaccesssnapshot.HasWorkItemWith(ticket.TenantIDEQ(tenantID), ticket.RecordClassEQ(creation.RecordClassServiceRequestItem), ticket.DeletedAtIsNil())).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &accessgrant.ApprovalSnapshot{PolicyID: row.PolicyID, PolicyVersion: row.PolicyVersion, Provider: accessgrant.Provider(row.Provider), ExternalSystem: row.ExternalSystem, SubjectID: row.SubjectID, GroupID: row.GroupID, DurationKey: row.DurationKey, DurationSeconds: row.DurationSeconds}, nil
}
func (s *Service) ReadApprovedAccess(ctx context.Context, client *ent.Client, tenantID, itemID int, task *ent.ProcessTask) (*accessgrant.ApprovedContext, error) {
	snapshot, err := s.ReadAccessSnapshot(ctx, client, tenantID, itemID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, &accessgrant.BlockedError{Code: "snapshot_unavailable"}
	}
	item, err := client.Ticket.Query().Where(ticket.IDEQ(itemID), ticket.TenantIDEQ(tenantID), ticket.RecordClassEQ(creation.RecordClassServiceRequestItem), ticket.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		return nil, err
	}
	if item.Status == "cancelled" || item.Status == "rejected" || item.Status == "closed" || item.Status == "resolved" {
		return nil, &accessgrant.BlockedError{Code: "request_not_executable"}
	}
	validIdentity, err := client.ExternalIdentity.Query().Where(externalidentity.TenantIDEQ(tenantID), externalidentity.UserIDEQ(item.RequesterID), externalidentity.ProviderEQ(string(snapshot.Provider)), externalidentity.WorkspaceEQ(snapshot.ExternalSystem), externalidentity.SubjectEQ(snapshot.SubjectID), externalidentity.ActiveEQ(true), externalidentity.HasUserWith(user.TenantIDEQ(tenantID), user.ActiveEQ(true))).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if !validIdentity {
		return nil, &accessgrant.BlockedError{Code: "requester_identity_inactive"}
	}
	if task.CallbackAction != accessgrant.Capability || task.CallbackConfigRef != fmt.Sprint(snapshot.PolicyID) {
		return nil, &accessgrant.BlockedError{Code: "policy_mismatch"}
	}
	progress, err := service.ReadWorkflowFulfillment(ctx, client, tenantID, itemID)
	if err != nil {
		return nil, err
	}
	if progress.State != "fulfilling" || progress.DelegatedTaskID != task.ID || len(progress.Approvals) == 0 {
		return nil, &accessgrant.BlockedError{Code: "approval_not_executable"}
	}
	return &accessgrant.ApprovedContext{ApprovalSnapshot: *snapshot, Approvals: progress.Approvals}, nil
}

// ValidateManualProvisioning keeps policy-governed access on its verified workflow.
// The frozen snapshot remains authoritative even when catalog configuration changes.
func (s *Service) ValidateManualProvisioning(ctx context.Context, client *ent.Client, tenantID, itemID int) error {
	snapshot, err := s.ReadAccessSnapshot(ctx, client, tenantID, itemID)
	if err != nil {
		return err
	}
	if snapshot != nil {
		return &accessgrant.BlockedError{Code: "managed_access_requires_verified_delegation"}
	}
	// Legacy requests missing the required frozen terms also cannot bypass a
	// configured access policy via the generic provisioning surface.
	request, err := client.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(itemID), servicerequest.HasWorkItemWith(ticket.TenantIDEQ(tenantID), ticket.RecordClassEQ("service_request_item"), ticket.DeletedAtIsNil())).Only(ctx)
	if err != nil {
		return err
	}
	policy, err := service_catalog.ReadAccessPolicy(ctx, client, tenantID, request.CatalogID)
	if err != nil {
		return err
	}
	if policy != nil {
		return &accessgrant.BlockedError{Code: "access_snapshot_unavailable"}
	}
	return nil
}

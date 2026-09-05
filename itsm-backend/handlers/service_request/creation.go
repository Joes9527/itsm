package service_request

import (
	"context"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/service_catalog"
	"math/big"
	"net"
	"net/mail"
	"time"
)

type requestCreation struct {
	Input                creation.ServiceRequestInput
	ExpireAt, ExpectedAt *time.Time
	Quantity             int
	Context              map[string]any
	CIID                 int
}

func (*Service) RecordClass() string { return creation.RecordClassServiceRequestItem }
func (s *Service) Prepare(ctx context.Context, tx *ent.Tx, in creation.ResolvedIntake) (*creation.CreationPlan, error) {
	if in.Catalog == nil || in.Catalog.TargetClass != creation.RecordClassServiceRequestItem {
		return nil, creation.NewDomainValidationFailed("requested item requires a resolved service catalog", nil)
	}
	input := creation.ServiceRequestInput{}
	if in.Command.ServiceRequest != nil {
		input = *in.Command.ServiceRequest
	}
	if input.Amount != "" {
		amount, ok := new(big.Rat).SetString(string(input.Amount))
		if !ok || amount.Sign() < 0 {
			return nil, creation.NewDomainValidationFailed("amount must be nonnegative", nil)
		}
	}
	expire, err := creation.ParseOptionalTime(input.ExpireAt, "serviceRequest.expireAt")
	if err != nil {
		return nil, err
	}
	expected, err := creation.ParseOptionalTime(input.ExpectedAt, "serviceRequest.expectedAt")
	if err != nil {
		return nil, err
	}
	if service_catalog.RequiresInfraFields(in.Catalog.ServiceType) {
		if !input.ComplianceAck {
			return nil, creation.NewDomainValidationFailed("compliance acknowledgement is required", nil)
		}
		if expire == nil || !expire.After(time.Now()) {
			return nil, creation.NewDomainValidationFailed("future expiration is required", nil)
		}
		if input.NeedsPublicIP && len(input.SourceIPWhitelist) == 0 {
			return nil, creation.NewDomainValidationFailed("public IP requires source whitelist", nil)
		}
		switch input.DataClassification {
		case "public", "internal", "confidential", "restricted":
		default:
			return nil, creation.NewDomainValidationFailed("invalid data classification", nil)
		}
	}
	for _, address := range input.SourceIPWhitelist {
		if net.ParseIP(address) == nil {
			if _, _, err := net.ParseCIDR(address); err != nil {
				return nil, creation.NewDomainValidationFailed("invalid IP whitelist entry", err)
			}
		}
	}
	if input.ContactEmail != "" {
		parsed, err := mail.ParseAddress(input.ContactEmail)
		if err != nil || parsed.Address != input.ContactEmail {
			return nil, creation.NewDomainValidationFailed("invalid contact email", err)
		}
	}
	quantity := 1
	if input.Quantity != nil {
		quantity = *input.Quantity
	}
	if quantity < 1 || quantity > 1000 {
		return nil, creation.NewDomainValidationFailed("quantity must be between 1 and 1000", nil)
	}
	priority := in.Command.Priority
	if priority == "" {
		priority = "medium"
	}
	switch priority {
	case "low", "medium", "high", "critical", "urgent":
	default:
		return nil, creation.NewDomainValidationFailed("invalid service request priority", nil)
	}
	if s.chainResolver == nil {
		return nil, creation.NewInternalFailure("approval policy resolver is required", nil)
	}
	chain, err := s.chainResolver.ResolveServiceRequestCreation(ctx, tx, in.Identity.TenantID, input.Amount)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not resolve approval chain", err)
	}
	workflowContext := map[string]any{}
	if chain != nil && len(chain.Steps) > 0 {
		if in.Workflow.NoProcess {
			return nil, creation.NewWorkflowBindingRequired("resolved approvals require a workflow", nil)
		}
		workflowContext["_approval_chain"] = chain.Steps
	}
	if input.Amount != "" {
		workflowContext["amount"] = input.Amount
	}
	if input.CloudResourceRefID != nil {
		workflowContext["cloud_resource_ref_id"] = *input.CloudResourceRefID
	}
	if in.Catalog.ConfigurationItemTypeID != nil {
		for _, ci := range in.ConfigurationItems {
			if ci.CiTypeID != *in.Catalog.ConfigurationItemTypeID {
				return nil, creation.NewDomainValidationFailed("linked CI type does not match catalog", nil)
			}
		}
	}
	ciID := 0
	if input.CloudResourceRefID != nil {
		for _, ci := range in.ConfigurationItems {
			if ci.CloudResourceRefID == *input.CloudResourceRefID {
				ciID = ci.ID
				break
			}
		}
	}
	if ciID == 0 && len(in.CIIDs) == 1 {
		ciID = in.CIIDs[0]
	}
	if len(in.CIIDs) > 1 {
		return nil, creation.NewDomainValidationFailed("requested item supports one linked CI", nil)
	}
	plan := creation.NewPlan(in, "new", priority, "service_catalog")
	plan.ProfessionalInput = requestCreation{Input: input, ExpireAt: expire, ExpectedAt: expected, Quantity: quantity, Context: workflowContext, CIID: ciID}
	return plan, nil
}
func (*Service) CreateExtension(ctx context.Context, tx *ent.Tx, item *ent.Ticket, plan *creation.CreationPlan) (*creation.ProfessionalReference, error) {
	prepared, ok := plan.ProfessionalInput.(requestCreation)
	if !ok {
		return nil, creation.NewInternalFailure("service request creation plan is invalid", nil)
	}
	input := prepared.Input
	create := tx.ServiceRequest.Create().SetTicketID(item.ID).SetCatalogID(plan.Resolved.Catalog.ID).
		SetTenantID(item.TenantID).SetRequesterID(item.RequesterID).SetCostCenter(input.CostCenter).
		SetDataClassification(input.DataClassification).SetNeedsPublicIP(input.NeedsPublicIP).SetSourceIPWhitelist(input.SourceIPWhitelist).
		SetComplianceAck(input.ComplianceAck).SetContactName(input.ContactName).SetContactEmail(input.ContactEmail).SetQuantity(prepared.Quantity).
		SetNillableExpireAt(prepared.ExpireAt).SetNillableExpectedAt(prepared.ExpectedAt).SetFormData(prepared.Context)
	if prepared.CIID > 0 {
		create.SetCiID(prepared.CIID)
	}
	record, err := create.Save(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not create service request extension", err)
	}
	return &creation.ProfessionalReference{Type: "service_request", ID: record.ID}, nil
}

var _ creation.ProfessionalCreator = (*Service)(nil)

package intake

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"itsm-backend/ent"
	"itsm-backend/handlers/service_catalog"
)

type ServiceRequestExtensionPlan struct {
	CatalogID          int
	CIID               int
	CostCenter         string
	DataClassification string
	NeedsPublicIP      bool
	SourceIPWhitelist  []string
	ExpireAt           *time.Time
	ComplianceAck      bool
	ContactName        string
	ContactEmail       string
	Quantity           int
	ExpectedAt         *time.Time
	ApprovalSnapshot   map[string]any
}

type ServiceRequestItemCreator struct{}

func NewServiceRequestItemCreator() *ServiceRequestItemCreator { return &ServiceRequestItemCreator{} }

func (c *ServiceRequestItemCreator) RecordClass() string { return RecordClassServiceRequestItem }

func (c *ServiceRequestItemCreator) Prepare(_ context.Context, tx *ent.Tx, in ResolvedIntake) (*CreationPlan, error) {
	if tx == nil {
		return nil, NewInternalFailure("service request transaction is required", nil)
	}
	if in.RecordClass != RecordClassServiceRequestItem || in.Catalog == nil || in.Catalog.TargetClass != RecordClassServiceRequestItem {
		return nil, NewUnsupportedRecordClass("service request creator requires a service_request_item catalog", nil)
	}
	values := in.Command.FormValues
	professional := ServiceRequestExtensionPlan{
		CatalogID:          in.Catalog.ID,
		CostCenter:         stringValue(values, "cost_center"),
		DataClassification: stringValue(values, "data_classification"),
		NeedsPublicIP:      boolValue(values, "needs_public_ip"),
		SourceIPWhitelist:  stringSliceValue(values, "source_ip_whitelist"),
		ComplianceAck:      boolValue(values, "compliance_ack"),
		ContactName:        stringValue(values, "contact_name"),
		ContactEmail:       stringValue(values, "contact_email"),
		Quantity:           intValue(values, "quantity"),
	}
	if professional.DataClassification == "" {
		professional.DataClassification = "internal"
	}
	if professional.Quantity <= 0 {
		professional.Quantity = 1
	}
	var err error
	professional.ExpireAt, err = optionalTimeValue(values, "expire_at")
	if err != nil {
		return nil, NewDomainValidationFailed("expire_at must be an RFC3339 timestamp", err)
	}
	professional.ExpectedAt, err = optionalTimeValue(values, "expected_at")
	if err != nil {
		return nil, NewDomainValidationFailed("expected_at must be an RFC3339 timestamp", err)
	}
	if len(in.CIIDs) == 1 {
		professional.CIID = in.CIIDs[0]
	} else if len(in.CIIDs) > 1 {
		return nil, NewDomainValidationFailed("service request supports at most one primary configuration item", nil)
	}
	if err := validateInfrastructureRequest(in.Catalog.ServiceType, professional); err != nil {
		return nil, err
	}
	return &CreationPlan{
		Resolved: in,
		WorkItem: WorkItemDraft{
			TenantID: in.Identity.TenantID, ActorID: in.Identity.ActorID, RequesterID: in.Identity.RequesterID,
			RecordClass: RecordClassServiceRequestItem, Title: in.Command.Title, Description: in.Command.Description,
			Status: "open", Priority: "medium", Source: "service_catalog", CategoryID: copyInt(in.CTI.ItemID), SLADefinitionID: copyInt(in.SLADefinitionID),
		},
		ProfessionalInput: professional,
	}, nil
}

func (c *ServiceRequestItemCreator) CreateExtension(ctx context.Context, tx *ent.Tx, workItem *ent.Ticket, plan *CreationPlan) (*ProfessionalReference, error) {
	if tx == nil || workItem == nil || plan == nil {
		return nil, NewInternalFailure("service request extension transaction, work item, and plan are required", nil)
	}
	input, ok := plan.ProfessionalInput.(ServiceRequestExtensionPlan)
	if !ok {
		return nil, NewDomainValidationFailed("service request creation plan is invalid", nil)
	}
	create := tx.ServiceRequest.Create().
		SetTenantID(plan.WorkItem.TenantID).
		SetTicketID(workItem.ID).
		SetCatalogID(input.CatalogID).
		SetRequesterID(plan.WorkItem.RequesterID).
		SetCostCenter(input.CostCenter).
		SetDataClassification(input.DataClassification).
		SetNeedsPublicIP(input.NeedsPublicIP).
		SetSourceIPWhitelist(input.SourceIPWhitelist).
		SetComplianceAck(input.ComplianceAck).
		SetContactName(input.ContactName).
		SetContactEmail(input.ContactEmail).
		SetQuantity(input.Quantity)
	if input.CIID > 0 {
		create.SetCiID(input.CIID)
	}
	if input.ExpireAt != nil {
		create.SetExpireAt(*input.ExpireAt)
	}
	if input.ExpectedAt != nil {
		create.SetExpectedAt(*input.ExpectedAt)
	}
	if len(input.ApprovalSnapshot) > 0 {
		create.SetFormData(map[string]any{"_approval_chain": input.ApprovalSnapshot})
	}
	created, err := create.Save(ctx)
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not create service request extension", err)
	}
	return &ProfessionalReference{Type: "service_request", ID: created.ID}, nil
}

func validateInfrastructureRequest(serviceType string, input ServiceRequestExtensionPlan) error {
	if !service_catalog.RequiresInfraFields(serviceType) {
		return nil
	}
	if !input.ComplianceAck {
		return NewDomainValidationFailed("compliance acknowledgement is required", nil)
	}
	if input.NeedsPublicIP && len(input.SourceIPWhitelist) == 0 {
		return NewDomainValidationFailed("source IP whitelist is required for public access", nil)
	}
	for _, address := range input.SourceIPWhitelist {
		if net.ParseIP(address) == nil {
			if _, _, err := net.ParseCIDR(address); err != nil {
				return NewDomainValidationFailed("source IP whitelist contains an invalid address", err)
			}
		}
	}
	if input.ExpireAt == nil || !input.ExpireAt.After(time.Now()) {
		return NewDomainValidationFailed("a future expiration time is required", nil)
	}
	switch input.DataClassification {
	case "public", "internal", "confidential", "restricted":
		return nil
	default:
		return NewDomainValidationFailed("data classification is invalid", nil)
	}
}

func stringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func boolValue(values map[string]any, key string) bool {
	value, _ := values[key].(bool)
	return value
}

func intValue(values map[string]any, key string) int {
	switch value := values[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(value))
		return parsed
	default:
		return 0
	}
}

func stringSliceValue(values map[string]any, key string) []string {
	switch value := values[key].(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		result := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if ok && strings.TrimSpace(text) != "" {
				result = append(result, strings.TrimSpace(text))
			}
		}
		return result
	default:
		return nil
	}
}

func optionalTimeValue(values map[string]any, key string) (*time.Time, error) {
	text := stringValue(values, key)
	if text == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

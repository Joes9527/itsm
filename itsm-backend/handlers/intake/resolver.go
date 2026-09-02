package intake

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/configurationitem"
	"itsm-backend/ent/fielddefinition"
	"itsm-backend/ent/sladefinition"
	"itsm-backend/ent/ticketcategory"
	"itsm-backend/ent/user"
	"itsm-backend/handlers/service_catalog"
	itsmservice "itsm-backend/service"
)

const intakeResolverVersion = "intake-resolver-v1"

type PermissionChecker interface {
	HasPermission(client *ent.Client, identity Identity, resource, action string) bool
}

type PermissionCheckFunc func(client *ent.Client, identity Identity, resource, action string) bool

func (f PermissionCheckFunc) HasPermission(client *ent.Client, identity Identity, resource, action string) bool {
	return f(client, identity, resource, action)
}

type defaultPermissionChecker struct{}

func (defaultPermissionChecker) HasPermission(client *ent.Client, identity Identity, resource, action string) bool {
	return authorization.HasResourcePermission(client, identity.Role, resource, action, identity.TenantID)
}

type IntakeWorkflowResolver interface {
	ResolveIntakeBinding(ctx context.Context, tx *ent.Tx, tenantID int, recordClass string, catalogDefinitionKey string) (*itsmservice.IntakeProcessBinding, error)
}

type Resolver struct {
	workflow   IntakeWorkflowResolver
	permission PermissionChecker
}

func NewResolver(workflow IntakeWorkflowResolver, permission PermissionChecker) *Resolver {
	if permission == nil {
		permission = defaultPermissionChecker{}
	}
	return &Resolver{workflow: workflow, permission: permission}
}

func (r *Resolver) Resolve(ctx context.Context, tx *ent.Tx, identity Identity, command CreateWorkItemCommand) (*ResolvedIntake, error) {
	if tx == nil {
		return nil, NewInternalFailure("intake transaction is required", nil)
	}
	if r.workflow == nil {
		return nil, NewInternalFailure("workflow binding resolver is required", nil)
	}
	if err := identity.ValidateCommand(command); err != nil {
		return nil, err
	}
	if identity.ActorID != identity.RequesterID {
		return nil, NewPermissionDenied("requester must match the authenticated actor", nil)
	}
	if err := validateBaseCommand(command); err != nil {
		return nil, err
	}
	client := tx.Client()
	active, err := client.User.Query().Where(
		user.IDEQ(identity.ActorID),
		user.TenantIDEQ(identity.TenantID),
		user.ActiveEQ(true),
	).Exist(ctx)
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not validate intake actor", err)
	}
	if !active {
		return nil, NewAuthenticationRequired("authenticated intake user is inactive or unavailable", nil)
	}
	if !r.permission.HasPermission(client, identity, "intake", "create") {
		return nil, NewPermissionDenied("intake create permission is required", nil)
	}

	resolved := &ResolvedIntake{
		Identity:        identity,
		Command:         command,
		RecordClass:     RecordClassIncident,
		CIIDs:           append([]int(nil), command.CIIDs...),
		ResolverVersion: intakeResolverVersion,
	}
	if command.IntakeKind == IntakeKindCatalogItem {
		if !r.permission.HasPermission(client, identity, "service_catalog", "read") {
			return nil, NewPermissionDenied("service catalog visibility is required", nil)
		}
		catalog, err := service_catalog.NewEntRepository(client).GetActiveForIntake(ctx, tx, identity.TenantID, *command.CatalogItemID)
		if ent.IsNotFound(err) {
			return nil, NewReferenceNotFound("service catalog item was not found", err)
		}
		if err != nil {
			return nil, NewInfrastructureUnavailable("could not resolve service catalog item", err)
		}
		if catalog.TargetClass != RecordClassServiceRequestItem && catalog.TargetClass != RecordClassIncident {
			return nil, NewUnsupportedRecordClass("service catalog target class is unsupported", nil)
		}
		resolved.RecordClass = catalog.TargetClass
		resolved.Catalog = &ResolvedCatalog{
			ID: catalog.ID,
			// Version intentionally left unset -- main's ServiceCatalog domain
			// struct (handlers/service_catalog/entity.go) has no Version field
			// to source it from; ResolvedCatalog.Version stays the zero value
			// until that concept exists on main.
			TargetClass:             catalog.TargetClass,
			ServiceType:             catalog.ServiceType,
			DeliveryTime:            catalog.DeliveryTime,
			ProcessDefinitionKey:    strings.TrimSpace(catalog.ProcessDefinitionKey),
			ConfigurationItemTypeID: optionalPositiveInt(catalog.CITypeID),
			CloudServiceID:          optionalPositiveInt(catalog.CloudServiceID),
		}
	}

	targetResource := "incident"
	if resolved.RecordClass == RecordClassServiceRequestItem {
		targetResource = "service_request"
	}
	if !r.permission.HasPermission(client, identity, targetResource, "write") {
		return nil, NewPermissionDenied("target create permission is required", nil)
	}

	cti, err := r.resolveCTI(ctx, client, identity, command.CTI)
	if err != nil {
		return nil, err
	}
	resolved.CTI = cti
	configurationItems, err := r.resolveCIs(ctx, client, identity, command.CIIDs)
	if err != nil {
		return nil, err
	}
	resolved.ConfigurationItems = configurationItems
	if err := r.resolveForm(ctx, client, resolved); err != nil {
		return nil, err
	}

	catalogDefinitionKey := ""
	if resolved.Catalog != nil {
		catalogDefinitionKey = resolved.Catalog.ProcessDefinitionKey
	}
	binding, err := r.workflow.ResolveIntakeBinding(ctx, tx, identity.TenantID, resolved.RecordClass, catalogDefinitionKey)
	if errors.Is(err, itsmservice.ErrIntakeWorkflowBindingRequired) {
		return nil, NewWorkflowBindingRequired("an active workflow binding or explicit no-process decision is required", err)
	}
	if errors.Is(err, itsmservice.ErrIntakeRecordClassUnsupported) {
		return nil, NewUnsupportedRecordClass("record class is unsupported", err)
	}
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not resolve workflow binding", err)
	}
	resolved.Workflow.NoProcess = binding.NoProcess
	if !binding.NoProcess {
		definitionID := binding.DefinitionID
		resolved.Workflow.DefinitionID = &definitionID
		resolved.Workflow.DefinitionKey = binding.DefinitionKey
		resolved.Workflow.DefinitionVersion = binding.DefinitionVersion
	}
	if binding.SLAPolicyID != "" {
		slaID, parseErr := strconv.Atoi(binding.SLAPolicyID)
		if parseErr != nil || slaID <= 0 {
			return nil, NewDomainValidationFailed("workflow SLA policy reference is invalid", parseErr)
		}
		exists, queryErr := client.SLADefinition.Query().Where(
			sladefinition.IDEQ(slaID),
			sladefinition.TenantIDEQ(identity.TenantID),
			sladefinition.IsActiveEQ(true),
		).Exist(ctx)
		if queryErr != nil {
			return nil, NewInfrastructureUnavailable("could not resolve SLA policy", queryErr)
		}
		if !exists {
			return nil, NewReferenceNotFound("SLA policy was not found", nil)
		}
		resolved.SLADefinitionID = &slaID
	}
	return resolved, nil
}

func (r *Resolver) resolveCTI(ctx context.Context, client *ent.Client, identity Identity, input *CTIInput) (ResolvedCTI, error) {
	if input == nil {
		return ResolvedCTI{}, nil
	}
	if input.CategoryID == nil || input.TypeID == nil || input.ItemID == nil {
		return ResolvedCTI{}, NewDomainValidationFailed("category, type, and item must form a complete CTI chain", nil, FieldError{Field: "cti", Message: "all three levels are required"})
	}
	if !r.permission.HasPermission(client, identity, "ticket_category", "read") {
		return ResolvedCTI{}, NewPermissionDenied("ticket category visibility is required", nil)
	}
	rows, err := client.TicketCategory.Query().Where(
		ticketcategory.IDIn(*input.CategoryID, *input.TypeID, *input.ItemID),
		ticketcategory.TenantIDEQ(identity.TenantID),
		ticketcategory.IsActiveEQ(true),
	).All(ctx)
	if err != nil {
		return ResolvedCTI{}, NewInfrastructureUnavailable("could not resolve CTI", err)
	}
	if len(rows) != 3 {
		return ResolvedCTI{}, NewReferenceNotFound("CTI reference was not found", nil)
	}
	byID := make(map[int]*ent.TicketCategory, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	typeNode, typeOK := byID[*input.TypeID]
	itemNode, itemOK := byID[*input.ItemID]
	if _, categoryOK := byID[*input.CategoryID]; !categoryOK || !typeOK || !itemOK || typeNode.ParentID != *input.CategoryID || itemNode.ParentID != *input.TypeID {
		return ResolvedCTI{}, NewDomainValidationFailed("CTI hierarchy is invalid", nil, FieldError{Field: "cti", Message: "type and item must be descendants of the selected category"})
	}
	return ResolvedCTI{CategoryID: copyInt(input.CategoryID), TypeID: copyInt(input.TypeID), ItemID: copyInt(input.ItemID)}, nil
}

func (r *Resolver) resolveCIs(ctx context.Context, client *ent.Client, identity Identity, ids []int) ([]*ent.ConfigurationItem, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if !r.permission.HasPermission(client, identity, "cmdb", "read") {
		return nil, NewPermissionDenied("configuration item visibility is required", nil)
	}
	items, err := client.ConfigurationItem.Query().Where(
		configurationitem.IDIn(ids...),
		configurationitem.TenantIDEQ(identity.TenantID),
	).Order(ent.Asc(configurationitem.FieldID)).All(ctx)
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not resolve configuration items", err)
	}
	if len(items) != len(ids) {
		return nil, NewReferenceNotFound("configuration item was not found", nil)
	}
	return items, nil
}

func (r *Resolver) resolveForm(ctx context.Context, client *ent.Client, resolved *ResolvedIntake) error {
	if resolved.Catalog == nil {
		return nil
	}
	defs, err := client.FieldDefinition.Query().Where(
		fielddefinition.TenantIDEQ(resolved.Identity.TenantID),
		fielddefinition.EntityTypeEQ("service_catalog"),
		fielddefinition.EntityIDEQ(resolved.Catalog.ID),
		fielddefinition.IsActiveEQ(true),
	).Order(ent.Asc(fielddefinition.FieldSortOrder), ent.Asc(fielddefinition.FieldID)).All(ctx)
	if err != nil {
		return NewInfrastructureUnavailable("could not resolve service catalog form", err)
	}
	byName := make(map[string]*ent.FieldDefinition, len(defs))
	latest := time.Time{}
	for _, def := range defs {
		byName[def.Name] = def
		if def.UpdatedAt.After(latest) {
			latest = def.UpdatedAt
		}
		resolved.FieldDefinitions = append(resolved.FieldDefinitions, ResolvedFieldDefinition{
			ID: def.ID, Key: def.Name, DataType: def.FieldType, Required: def.Required, Options: append([]any(nil), def.Options...),
		})
		value, present := resolved.Command.FormValues[def.Name]
		if def.Required && (!present || value == nil || value == "") {
			return NewDomainValidationFailed("service catalog form validation failed", nil, FieldError{Field: "formValues." + def.Name, Message: "is required"})
		}
		if present {
			if validateErr := itsmservice.ValidateFieldValue(def, value); validateErr != nil {
				return NewDomainValidationFailed("service catalog form validation failed", validateErr, FieldError{Field: "formValues." + def.Name, Message: "has an invalid value"})
			}
		}
	}
	for key := range resolved.Command.FormValues {
		_, customField := byName[key]
		professionalField := resolved.RecordClass == RecordClassServiceRequestItem && isServiceRequestProfessionalField(key)
		if !customField && !professionalField {
			return NewDomainValidationFailed("service catalog form validation failed", nil, FieldError{Field: "formValues." + key, Message: "is not defined by this catalog item"})
		}
	}
	if !latest.IsZero() {
		resolved.Catalog.FormSchemaVersion = fmt.Sprintf("%d:%s", len(defs), latest.UTC().Format(time.RFC3339Nano))
	}
	return nil
}

func isServiceRequestProfessionalField(key string) bool {
	switch key {
	case "cost_center", "data_classification", "needs_public_ip", "source_ip_whitelist", "expire_at", "compliance_ack", "contact_name", "contact_email", "quantity", "expected_at":
		return true
	default:
		return false
	}
}

func optionalPositiveInt(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
}

package service

import (
	"context"
	"encoding/json"
	"itsm-backend/ent"
	"itsm-backend/ent/processbinding"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/sladefinition"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strconv"
)

// These are public routing declarations, not storage entities. Mutable actor
// membership and clocks never form part of a user's confirmed definition.
type creationBindingDefinition struct {
	ID                                                  int
	BusinessType, BusinessSubType, ProcessDefinitionKey string
	ProcessVersion                                      int
	IsDefault                                           bool
	Priority, DepartmentID, TeamID, CategoryID          int
	Scenario, Category, ApprovalChainID, SLAPolicyID    string
	Conditions, Overrides                               map[string]interface{}
}
type creationSLADefinition struct {
	ID                                         int
	ServiceType, Priority                      string
	CategoryIDs                                []int
	ResponseTime, ResolutionTime               int
	BusinessHours, EscalationRules, Conditions map[string]interface{}
	ExcludeWeekends, ExcludeHolidays           bool
}
type creationWorkflowConfiguration struct {
	Bindings     []creationBindingDefinition
	Definitions  []ProcessDefinitionIdentity
	SLA          []creationSLADefinition
	Capabilities []json.RawMessage
}
type creationConfigurationRecords struct {
	bindings    []*ent.ProcessBinding
	definitions []*ent.ProcessDefinition
	sla         []*ent.SLADefinition
}

// loadCreationConfiguration is also used by publication validation. Missing or
// invalid declarations remain inspectable in a draft; validation is separate.
func loadCreationConfiguration(ctx context.Context, tx *ent.Tx, tenantID int, class, key string) (creationConfigurationRecords, error) {
	result := creationConfigurationRecords{}
	if key == "" {
		business := map[string]string{"generic": "ticket", "incident": "incident", "problem": "problem", "change_request": "change", "service_request_item": "service_request"}[class]
		if business != "" {
			rows, err := tx.ProcessBinding.Query().Where(processbinding.TenantIDEQ(tenantID), processbinding.BusinessTypeEQ(business), processbinding.IsActiveEQ(true)).Order(ent.Asc(processbinding.FieldID)).All(ctx)
			if err != nil {
				return result, err
			}
			result.bindings = rows
		}
	}
	refs := []struct {
		key     string
		version int
	}{}
	if key != "" {
		refs = append(refs, struct {
			key     string
			version int
		}{key, 0})
	}
	slaIDs := []int{}
	for _, b := range result.bindings {
		if b.Conditions["no_process"] != true {
			refs = append(refs, struct {
				key     string
				version int
			}{b.ProcessDefinitionKey, b.ProcessVersion})
		}
		if id, err := strconv.Atoi(b.SLAPolicyID); err == nil && id > 0 {
			slaIDs = append(slaIDs, id)
		}
	}
	seen := map[int]bool{}
	for _, ref := range refs {
		q := tx.ProcessDefinition.Query().Where(processdefinition.TenantIDEQ(tenantID), processdefinition.KeyEQ(ref.key), processdefinition.IsActiveEQ(true))
		if ref.version <= 0 {
			q = q.Where(processdefinition.IsLatestEQ(true))
		}
		rows, err := q.Order(ent.Desc(processdefinition.FieldIsLatest), ent.Desc(processdefinition.FieldDeployedAt), ent.Desc(processdefinition.FieldID)).All(ctx)
		if err != nil {
			return result, err
		}
		for _, row := range rows {
			major, err := creationProcessDefinitionMajorVersion(row.Version)
			if ref.version > 0 && (err != nil || major != ref.version) {
				continue
			}
			if !seen[row.ID] {
				result.definitions = append(result.definitions, row)
				seen[row.ID] = true
			}
			break
		}
	}
	if len(slaIDs) > 0 {
		rows, err := tx.SLADefinition.Query().Where(sladefinition.TenantIDEQ(tenantID), sladefinition.IDIn(slaIDs...), sladefinition.IsActiveEQ(true)).Order(ent.Asc(sladefinition.FieldID)).All(ctx)
		if err != nil {
			return result, err
		}
		result.sla = rows
	}
	return result, nil
}

func (*ProcessBindingService) CreationConfigurationRevision(ctx context.Context, tx *ent.Tx, tenantID int, class, key string, engine *CustomProcessEngine) (string, error) {
	records, err := loadCreationConfiguration(ctx, tx, tenantID, class, key)
	if err != nil {
		return "", creation.NewInfrastructureUnavailable("could not load workflow configuration", err)
	}
	projection := creationWorkflowConfiguration{Bindings: []creationBindingDefinition{}, Definitions: []ProcessDefinitionIdentity{}, SLA: []creationSLADefinition{}, Capabilities: []json.RawMessage{}}
	for _, b := range records.bindings {
		projection.Bindings = append(projection.Bindings, creationBindingDefinition{ID: b.ID, BusinessType: b.BusinessType, BusinessSubType: b.BusinessSubType, ProcessDefinitionKey: b.ProcessDefinitionKey, ProcessVersion: b.ProcessVersion, IsDefault: b.IsDefault, Priority: b.Priority, DepartmentID: b.DepartmentID, TeamID: b.TeamID, CategoryID: b.CategoryID, Scenario: b.Scenario, Category: b.Category, ApprovalChainID: b.ApprovalChainID, SLAPolicyID: b.SLAPolicyID, Conditions: b.Conditions, Overrides: b.Overrides})
	}
	for _, d := range records.definitions {
		projection.Definitions = append(projection.Definitions, FreezeProcessDefinition(d))
		if engine != nil {
			configs, err := engine.publicationCapabilityConfigurations(ctx, tx.Client(), tenantID, d)
			if err != nil {
				return "", err
			}
			projection.Capabilities = append(projection.Capabilities, configs...)
		}
	}
	for _, d := range records.sla {
		projection.SLA = append(projection.SLA, creationSLADefinition{ID: d.ID, ServiceType: d.ServiceType, Priority: d.Priority, CategoryIDs: d.CategoryIds, ResponseTime: d.ResponseTime, ResolutionTime: d.ResolutionTime, BusinessHours: d.BusinessHours, EscalationRules: d.EscalationRules, Conditions: d.Conditions, ExcludeWeekends: d.ExcludeWeekends, ExcludeHolidays: d.ExcludeHolidays})
	}
	return creation.ConfigurationRevision("workflow-config-v1", projection)
}

// ValidateCreationPublication checks every declared possible route, rather than
// inventing a requester or selecting a route with empty form values.
func (*ProcessBindingService) ValidateCreationPublication(ctx context.Context, tx *ent.Tx, tenantID int, class, key string, requiresApproval bool, engine *CustomProcessEngine) error {
	records, err := loadCreationConfiguration(ctx, tx, tenantID, class, key)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not validate workflow configuration", err)
	}
	if key == "" && len(records.bindings) == 0 {
		return creation.NewWorkflowBindingRequired("publication requires a declared process or no_process binding", nil)
	}
	validateRef := func(key string, version int) error {
		for _, d := range records.definitions {
			major, err := creationProcessDefinitionMajorVersion(d.Version)
			if err != nil {
				return creation.NewDomainValidationFailed("invalid process version", err)
			}
			if d.Key == key && (version <= 0 || major == version) {
				if engine == nil {
					return creation.NewInfrastructureUnavailable("publication process engine is required", nil)
				}
				return engine.ValidateDefinitionForPublication(ctx, tx.Client(), tenantID, d, requiresApproval)
			}
		}
		return creation.NewWorkflowBindingRequired("declared process version is unavailable", nil)
	}
	if key != "" {
		return validateRef(key, 0)
	}
	for _, b := range records.bindings {
		if err := validateRoutingConditions(b.Conditions); err != nil {
			return creation.NewDomainValidationFailed("invalid routing configuration", err)
		}
		if b.SLAPolicyID != "" {
			id, err := strconv.Atoi(b.SLAPolicyID)
			found := false
			for _, d := range records.sla {
				found = found || d.ID == id
			}
			if err != nil || id <= 0 || !found {
				return creation.NewDomainValidationFailed("declared SLA is unavailable in catalog tenant", err)
			}
		}
		if b.Conditions["no_process"] == true {
			if requiresApproval {
				return creation.NewDomainValidationFailed("approval requires a process", nil)
			}
			continue
		}
		if b.ProcessVersion <= 0 {
			return creation.NewDomainValidationFailed("positive process version is required", nil)
		}
		if err := validateRef(b.ProcessDefinitionKey, b.ProcessVersion); err != nil {
			return err
		}
	}
	return nil
}

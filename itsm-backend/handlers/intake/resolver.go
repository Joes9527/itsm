package intake

import (
	"context"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"
	"strconv"
)

type Resolver struct {
	catalog        creation.CatalogResolver
	workflow       creation.WorkflowResolver
	cis            creation.ConfigurationItemResolver
	classification creation.ClassificationResolver
}

func NewResolver(catalog creation.CatalogResolver, workflow creation.WorkflowResolver, cis creation.ConfigurationItemResolver, classification creation.ClassificationResolver) *Resolver {
	return &Resolver{catalog: catalog, workflow: workflow, cis: cis, classification: classification}
}
func (r *Resolver) Resolve(ctx context.Context, tx *ent.Tx, identity creation.Identity, command creation.CreateWorkItemCommand) (*creation.ResolvedIntake, error) {
	if r == nil || missingDependency(r.catalog) || missingDependency(r.workflow) || missingDependency(r.cis) || missingDependency(r.classification) {
		return nil, creation.NewInternalFailure("intake resolver is not fully configured", nil)
	}
	result := &creation.ResolvedIntake{Identity: identity, Command: command, RecordClass: command.RecordClass, ResolverVersion: "intake-resolver-v1"}
	var err error
	result.CTI, err = r.classification.ResolveCreationClassification(ctx, tx, identity, command)
	if err != nil {
		return nil, err
	}
	if command.CatalogItemID != nil {
		result.Catalog, result.FieldDefinitions, err = r.catalog.ResolveCreationCatalog(ctx, tx, identity, *command.CatalogItemID)
		if err != nil {
			return nil, err
		}
		if result.Catalog == nil || result.Catalog.Version == "" || result.Catalog.FormSchemaVersion == "" {
			return nil, creation.NewInternalFailure("catalog revision is required", nil)
		}
		if result.Catalog.TargetClass != command.RecordClass {
			return nil, creation.NewDomainValidationFailed("catalog target class does not match command", nil)
		}
		if result.Catalog.Version != command.CatalogVersion || result.Catalog.FormSchemaVersion != command.FormSchemaVersion {
			return nil, creation.NewIntakeError(creation.CatalogVersionConflict, "catalog or form changed after confirmation", nil)
		}
		result.Workflow, result.SLADefinitionID, err = r.workflow.ResolveCreationWorkflow(ctx, tx, *result, result.Catalog.ProcessDefinitionKey)
		if err != nil {
			return nil, err
		}
		if result.Catalog.RequiresApproval && result.Workflow.NoProcess {
			return nil, creation.NewWorkflowBindingRequired("approval catalog requires a workflow", nil)
		}
		if err = service.NewFieldDefinitionService(tx.Client()).ValidateCreationValues(ctx, tx, identity.TenantID, "service_catalog", result.Catalog.ID, command.FormValues); err != nil {
			return nil, err
		}
	} else {
		key := ""
		if command.Generic != nil {
			key = command.Generic.WorkflowDefinitionKey
		}
		result.Workflow, result.SLADefinitionID, err = r.workflow.ResolveCreationWorkflow(ctx, tx, *result, key)
		if err != nil {
			return nil, err
		}
		if command.Generic != nil && command.Generic.TemplateID != nil {
			fields := service.NewFieldDefinitionService(tx.Client())
			if err = fields.ValidateCreationValues(ctx, tx, identity.TenantID, "ticket_template", *command.Generic.TemplateID, command.FormValues); err != nil {
				return nil, err
			}
			result.FieldScope, err = fields.CreationFieldScope(ctx, tx, identity.TenantID, "ticket_template", *command.Generic.TemplateID)
			if err != nil {
				return nil, err
			}
		} else if len(command.FormValues) > 0 {
			return nil, creation.NewDomainValidationFailed("dynamic fields require a catalog or template definition", nil)
		}
	}
	ids := append([]int(nil), command.CIIDs...)
	if command.Change != nil {
		for _, raw := range command.Change.AffectedCIs {
			id, _ := strconv.Atoi(raw)
			ids = append(ids, id)
		}
	}
	var cloudRef *int
	if command.ServiceRequest != nil {
		cloudRef = command.ServiceRequest.CloudResourceRefID
	}
	result.ConfigurationItems, err = r.cis.ResolveCreationCIs(ctx, tx, identity, ids, cloudRef)
	if err != nil {
		return nil, err
	}
	for _, ci := range result.ConfigurationItems {
		result.CIIDs = append(result.CIIDs, ci.ID)
	}
	return result, nil
}

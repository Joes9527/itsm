package intake

import (
	"context"
	"encoding/json"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/tickettag"
	"itsm-backend/ent/tickettemplate"
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
	if err := resolveSharedCreationReferences(ctx, tx, identity, command); err != nil {
		return nil, err
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
		if identity.CatalogOptionKeys {
			result.Command.FormValues, err = service.ResolveCatalogOptionKeys(result.FieldDefinitions, command.FormValues)
			if err != nil {
				return nil, err
			}
		}
		if err = service.NewFieldDefinitionService(tx.Client()).ValidateCreationValues(ctx, tx, identity.TenantID, "service_catalog", result.Catalog.ID, result.Command.FormValues); err != nil {
			return nil, err
		}
	} else {
		if command.TemplateID != nil {
			fields := service.NewFieldDefinitionService(tx.Client())
			if err = fields.ValidateCreationValues(ctx, tx, identity.TenantID, "ticket_template", *command.TemplateID, command.FormValues); err != nil {
				return nil, err
			}
			result.FieldScope, err = fields.CreationFieldScope(ctx, tx, identity.TenantID, "ticket_template", *command.TemplateID)
			if err != nil {
				return nil, err
			}
		} else if len(command.FormValues) > 0 && len(command.AdHocFields) == 0 {
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

// ResolveWorkflow follows owning-domain preparation, so routing observes exactly
// the effective classification/priority that will be persisted.
func (r *Resolver) ResolveWorkflow(ctx context.Context, tx *ent.Tx, plan *creation.CreationPlan) error {
	key := ""
	if plan.Resolved.Catalog != nil {
		key = plan.Resolved.Catalog.ProcessDefinitionKey
	} else {
		key = plan.Resolved.Command.WorkflowDefinitionKey
	}
	workflow, slaID, err := r.workflow.ResolveCreationWorkflow(ctx, tx, plan, key)
	if err != nil {
		return err
	}
	if workflow.NoProcess && (plan.RequiresWorkflow || (plan.Resolved.Catalog != nil && plan.Resolved.Catalog.RequiresApproval)) {
		return creation.NewWorkflowBindingRequired("resolved approvals require a workflow", nil)
	}
	plan.Resolved.Workflow = workflow
	plan.Resolved.SLADefinitionID = slaID
	plan.WorkItem.SLADefinitionID = slaID
	return nil
}

func resolveSharedCreationReferences(ctx context.Context, tx *ent.Tx, identity creation.Identity, command creation.CreateWorkItemCommand) error {
	if command.TemplateID != nil {
		if err := authorization.RequireCurrentPermission(ctx, tx, identity, "ticket", "read"); err != nil {
			return err
		}
		template, err := tx.TicketTemplate.Query().Where(tickettemplate.IDEQ(*command.TemplateID), tickettemplate.TenantIDEQ(identity.TenantID), tickettemplate.IsActiveEQ(true)).Only(ctx)
		if ent.IsNotFound(err) {
			return creation.NewReferenceNotFound("template is unavailable", nil)
		}
		if err != nil {
			return creation.NewInfrastructureUnavailable("could not resolve template", err)
		}
		// Legacy JSON steps have no registered executor. Only an absent/null/empty
		// list delegates to the authoritative process-binding resolver.
		if len(template.WorkflowSteps) > 0 {
			var steps []json.RawMessage
			if err := json.Unmarshal(template.WorkflowSteps, &steps); err != nil || len(steps) > 0 {
				return creation.NewWorkflowBindingRequired("ticket template workflow_steps are unsupported; configure a process binding", err)
			}
		}
	}
	if command.ParentTicketID != nil {
		if err := authorization.RequireCurrentPermission(ctx, tx, identity, "ticket", "read"); err != nil {
			return err
		}
		exists, err := tx.Ticket.Query().Where(ticket.IDEQ(*command.ParentTicketID), ticket.TenantIDEQ(identity.TenantID), ticket.DeletedAtIsNil()).Exist(ctx)
		if err != nil {
			return creation.NewInfrastructureUnavailable("could not resolve parent work item", err)
		}
		if !exists {
			return creation.NewReferenceNotFound("parent work item is unavailable", nil)
		}
	}
	if len(command.TagIDs) > 0 {
		if err := authorization.RequireCurrentPermission(ctx, tx, identity, "ticket", "read"); err != nil {
			return err
		}
		count, err := tx.TicketTag.Query().Where(tickettag.IDIn(command.TagIDs...), tickettag.TenantIDEQ(identity.TenantID)).Count(ctx)
		if err != nil {
			return creation.NewInfrastructureUnavailable("could not resolve tags", err)
		}
		if count != len(command.TagIDs) {
			return creation.NewReferenceNotFound("tag is outside tenant", nil)
		}
	}
	return nil
}

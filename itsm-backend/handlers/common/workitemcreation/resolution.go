package workitemcreation

import (
	"context"
	"itsm-backend/ent"
)

// Owning services resolve their domain references through the caller transaction.
// Intake never constructs another domain's repository.
type CatalogResolver interface {
	ResolveCreationCatalog(context.Context, *ent.Tx, Identity, int) (*ResolvedCatalog, []ResolvedFieldDefinition, error)
}
type WorkflowResolver interface {
	ResolveCreationWorkflow(context.Context, *ent.Tx, *CreationPlan, string) (ResolvedWorkflowBinding, *int, error)
}
type ConfigurationItemResolver interface {
	ResolveCreationCIs(context.Context, *ent.Tx, Identity, []int, *int) ([]*ent.ConfigurationItem, error)
}
type ClassificationResolver interface {
	ResolveCreationClassification(context.Context, *ent.Tx, Identity, CreateWorkItemCommand) (ResolvedCTI, error)
}

package schema_test

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestWorkItemRelation_UniqueConstraint(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:workitemrelation?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	_, err := client.WorkItemRelation.Create().
		SetTenantID(1).
		SetSourceWorkItemID(10).
		SetTargetWorkItemID(20).
		SetRelationType("related_to").
		SetCreatedByID(1).
		Save(ctx)
	require.NoError(t, err)

	// Same (tenant, source, target, relation_type) tuple must be rejected.
	_, err = client.WorkItemRelation.Create().
		SetTenantID(1).
		SetSourceWorkItemID(10).
		SetTargetWorkItemID(20).
		SetRelationType("related_to").
		SetCreatedByID(1).
		Save(ctx)
	require.Error(t, err)

	// A different relation_type between the same two WorkItems is allowed.
	_, err = client.WorkItemRelation.Create().
		SetTenantID(1).
		SetSourceWorkItemID(10).
		SetTargetWorkItemID(20).
		SetRelationType("duplicate_of").
		SetCreatedByID(1).
		Save(ctx)
	require.NoError(t, err)
}

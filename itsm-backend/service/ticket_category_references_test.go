package service

import (
	"testing"

	"itsm-backend/ent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ctiTicketWithCategory creates a work item, optionally classified, so rule and
// reference tests share one fixture shape.
func ctiTicketWithCategory(t *testing.T, client *ent.Client, tenantID, categoryID int, number string) *ent.Ticket {
	t.Helper()
	user := client.User.Create().
		SetTenantID(tenantID).
		SetUsername("cti-" + number).
		SetName("CTI User").
		SetRole("agent").
		SetEmail(number + "@example.test").
		SetPasswordHash("unused").
		SaveX(t.Context())
	create := client.Ticket.Create().
		SetTenantID(tenantID).
		SetRequesterID(user.ID).
		SetTitle(number).
		SetTicketNumber(number).
		SetStatus("open")
	if categoryID > 0 {
		create = create.SetCategoryID(categoryID)
	}
	return create.SaveX(t.Context())
}

func ctiReferencesFor(t *testing.T, client *ent.Client, tenantID int, path []CTINode) CTIReferences {
	t.Helper()
	tx, err := client.Tx(t.Context())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	references, err := countCTIReferences(t.Context(), tx, tenantID, path)
	require.NoError(t, err)
	return references
}

// The reference scanner is the authority for "is this node still in use".
// Protection must follow current references only (removing the reference frees
// the node), and published catalogs are reported separately so they can block
// deactivation without blocking every maintenance action.
func TestCountCTIReferencesTracksCurrentReferencesOnly(t *testing.T) {
	client, categories, tenant := ctiClientFixture(t)
	root := createCTI(t, categories, tenant, 0, "refs-root")
	child := createCTI(t, categories, tenant, root, "refs-child")
	leaf := createCTI(t, categories, tenant, child, "refs-leaf")
	path, err := categories.GetCategoryPath(t.Context(), tenant, leaf)
	require.NoError(t, err)
	require.Len(t, path, 3)

	baseline := ctiReferencesFor(t, client, tenant, path)
	assert.False(t, baseline.Blocking())
	assert.False(t, baseline.PublishedCatalogBlocking())

	item := ctiTicketWithCategory(t, client, tenant, leaf, "REFS-ITEM")
	referenced := ctiReferencesFor(t, client, tenant, path)
	assert.True(t, referenced.Blocking(), "a referencing work item must block delete/move")
	assert.Equal(t, 1, referenced.WorkItems)

	client.Ticket.UpdateOneID(item.ID).ClearCategoryID().ExecX(t.Context())
	assert.False(t, ctiReferencesFor(t, client, tenant, path).Blocking(),
		"protection must follow current references, not history")
}

func TestCountCTIReferencesReportsPublishedCatalogSeparately(t *testing.T) {
	client, categories, tenant := ctiClientFixture(t)
	leaf := createCTI(t, categories, tenant, 0, "refs-catalog")
	path, err := categories.GetCategoryPath(t.Context(), tenant, leaf)
	require.NoError(t, err)

	client.ServiceCatalog.Create().
		SetTenantID(tenant).
		SetName("refs published").
		SetTargetClass("service_request_item").
		SetStatus("enabled").
		SetDefaultTicketCategoryID(leaf).
		SaveX(t.Context())

	references := ctiReferencesFor(t, client, tenant, path)
	assert.True(t, references.PublishedCatalogBlocking())
	assert.Equal(t, 1, references.PublishedCatalogs)

	client.ServiceCatalog.Create().
		SetTenantID(tenant).
		SetName("refs draft").
		SetTargetClass("service_request_item").
		SetStatus("disabled").
		SetDefaultTicketCategoryID(leaf).
		SaveX(t.Context())

	drafts := ctiReferencesFor(t, client, tenant, path)
	assert.Equal(t, 1, drafts.PublishedCatalogs, "disabled catalogs must not count as published")
	assert.True(t, drafts.Catalogs > 1 || drafts.PublishedCatalogs == 1)
}

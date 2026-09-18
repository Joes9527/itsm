package service_catalog

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	creation "itsm-backend/handlers/common/workitemcreation"
)

// A publication failure must expose the underlying configuration cause instead
// of returning only the generic "configuration is incomplete" message.
func TestPublicationFailureExposesUnderlyingCause(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	tenant := client.Tenant.Create().SetName("Cause").SetCode(t.Name()).SetStatus("active").SaveX(ctx)
	svc := newCatalogPublisher(NewEntRepository(client), client, zap.NewNop().Sugar(), nil)

	_, err := svc.Create(ctx, tenant.ID, dto.CreateServiceCatalogRequest{
		Name: "SR", Category: "IT", Status: "enabled", TargetClass: "generic",
	})
	require.Error(t, err)

	var intakeErr *creation.IntakeError
	require.ErrorAs(t, err, &intakeErr)
	require.Equal(t, "catalog publication configuration is incomplete", intakeErr.Message)
	require.NotEmpty(t, intakeErr.FieldErrors, "publication failure must expose the underlying cause")
	require.Contains(t, intakeErr.FieldErrors[0].Message, "no_process binding")
}

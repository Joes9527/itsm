package change

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/handlers/shared/workitemmutation"
	executionfixture "itsm-backend/tests/fixtures/execution"
)

func TestChangeMetadataRequiresExplicitIdentity(t *testing.T) {
	owner := NewService(nil, nil, nil, executionfixture.Standard())
	_, err := owner.ApplyMetadata(context.Background(), MetadataCommand{ChangeID: 1, Patch: dto.UpdateChangeRequest{}})
	require.ErrorContains(t, err, "version")
	_, err = owner.ApplyMetadata(context.Background(), MetadataCommand{ChangeID: 1, Meta: workitemmutation.Meta{TenantID: 1, ActorID: 1, ExpectedVersion: 1, Source: "http"}, Patch: dto.UpdateChangeRequest{}})
	require.ErrorContains(t, err, "operationId")
}

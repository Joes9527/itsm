package change

import (
	"context"
	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/handlers/shared/workitemmutation"
	"testing"
)

func TestChangeMetadataRequiresExplicitIdentity(t *testing.T) {
	owner := NewService(nil, nil, nil)
	_, err := owner.ApplyMetadata(context.Background(), MetadataCommand{ChangeID: 1, Patch: dto.UpdateChangeRequest{}})
	require.ErrorContains(t, err, "version")
	_, err = owner.ApplyMetadata(context.Background(), MetadataCommand{ChangeID: 1, Meta: workitemmutation.Meta{TenantID: 1, ActorID: 1, ExpectedVersion: 1, Source: "http"}, Patch: dto.UpdateChangeRequest{}})
	require.ErrorContains(t, err, "operationId")
}

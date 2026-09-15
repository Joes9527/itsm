package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareBootstrapInfrastructureFailsClosedWithoutDatabase(t *testing.T) {
	require.ErrorContains(t, PrepareBootstrapInfrastructure(context.Background(), nil), "bootstrap database is required")
}

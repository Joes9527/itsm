package main

import (
	"context"
	"errors"
	"testing"

	"itsm-backend/internal/bootstrap"

	"github.com/stretchr/testify/require"
)

func TestRunReturnsWorkerConstructionError(t *testing.T) {
	want := errors.New("missing worker configuration")
	err := run(context.Background(), func() (*bootstrap.KAFWorkerApplication, error) {
		return nil, want
	})

	require.ErrorIs(t, err, want)
}

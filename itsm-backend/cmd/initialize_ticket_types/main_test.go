package main

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTicketTypesCLIRequiresExplicitApply(t *testing.T) {
	options, err := parseOptions([]string{"--tenant-id", "1", "--actor-id", "2"}, io.Discard)
	require.NoError(t, err)
	require.False(t, options.apply)
	options, err = parseOptions([]string{"--tenant-id", "1", "--actor-id", "2", "--apply"}, io.Discard)
	require.NoError(t, err)
	require.True(t, options.apply)
}

func TestTicketTypesCLIRejectsUnscopedAndUnknownInput(t *testing.T) {
	for _, args := range [][]string{nil, {"--tenant-id", "1"}, {"--tenant-id", "1", "--actor-id", "0"}, {"--tenant-id", "1", "--actor-id", "2", "unexpected"}, {"--tenant-id", "1", "--actor-id", "2", "--seed-all"}} {
		_, err := parseOptions(args, io.Discard)
		require.Error(t, err)
	}
}

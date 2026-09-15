package workitemassignment

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAssignmentRejectsInvalidCommandBeforePersistence(t *testing.T) {
	good := Command{WorkItemID: 1, TenantID: 2, ActorTenantID: 2, ActorID: 3, AssigneeID: 4, ExpectedVersion: 1, Source: "manual"}
	for _, change := range []func(*Command){
		func(c *Command) { c.WorkItemID = 0 }, func(c *Command) { c.TenantID = 0 },
		func(c *Command) { c.ActorTenantID = 0 }, func(c *Command) { c.ActorID = 0 },
		func(c *Command) { c.AssigneeID = -1 }, func(c *Command) { c.ExpectedVersion = 0 },
		func(c *Command) { c.Source = " " }, func(c *Command) { c.Source = "manual\nforged" },
	} {
		cmd := good
		change(&cmd)
		_, err := NewWriter(nil, nil).Apply(context.Background(), nil, cmd)
		require.ErrorIs(t, err, ErrInvalidCommand)
	}
}

package schema

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTicketNumberIsImmutableAndTenantScopedUnique(t *testing.T) {
	var immutable, fieldUnique, composite bool
	for _, f := range (Ticket{}).Fields() {
		if f.Descriptor().Name == "ticket_number" {
			immutable = f.Descriptor().Immutable
			fieldUnique = f.Descriptor().Unique
		}
	}
	for _, idx := range (Ticket{}).Indexes() {
		d := idx.Descriptor()
		if reflect.DeepEqual(d.Fields, []string{"tenant_id", "ticket_number"}) {
			composite = d.Unique
		}
		require.False(t, d.Unique && reflect.DeepEqual(d.Fields, []string{"ticket_number"}))
	}
	require.True(t, immutable)
	require.False(t, fieldUnique)
	require.True(t, composite)
}

func TestWorkItemNumberSequenceUsesOneTenantPeriodRow(t *testing.T) {
	var unique bool
	for _, idx := range (WorkItemNumberSequence{}).Indexes() {
		d := idx.Descriptor()
		unique = unique || d.Unique &&
			reflect.DeepEqual(d.Fields, []string{"tenant_id", "period"})
	}
	require.True(t, unique)
}

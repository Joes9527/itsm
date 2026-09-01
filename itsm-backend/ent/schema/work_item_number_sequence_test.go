package schema

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTicketNumberAndRecordClassAreImmutableAndTenantScopedUnique(t *testing.T) {
	var ticketNumberImmutable, recordClassImmutable, fieldUnique, composite bool
	for _, f := range (Ticket{}).Fields() {
		switch f.Descriptor().Name {
		case "ticket_number":
			ticketNumberImmutable = f.Descriptor().Immutable
			fieldUnique = f.Descriptor().Unique
		case "record_class":
			recordClassImmutable = f.Descriptor().Immutable
		}
	}
	for _, idx := range (Ticket{}).Indexes() {
		d := idx.Descriptor()
		if reflect.DeepEqual(d.Fields, []string{"tenant_id", "ticket_number"}) {
			composite = d.Unique
		}
		require.False(t, d.Unique && reflect.DeepEqual(d.Fields, []string{"ticket_number"}))
	}
	require.True(t, ticketNumberImmutable)
	require.True(t, recordClassImmutable)
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

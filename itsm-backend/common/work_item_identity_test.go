package common

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWorkItemLegacyIdentityProjection(t *testing.T) {
	for _, tt := range []struct{ input, class, subtype, output string }{
		{"incident", "incident", "", "incident"}, {"problem", "problem", "", "problem"}, {"change", "change_request", "", "change"}, {"change_request", "change_request", "", "change"}, {"service_request", "service_request_item", "", "service_request"}, {"service_request_item", "service_request_item", "", "service_request"}, {"custom", "generic", "custom", "custom"},
	} {
		class, subtype := WorkItemIdentityFilter(tt.input)
		require.Equal(t, tt.class, class)
		require.Equal(t, tt.subtype, subtype)
		require.Equal(t, tt.output, WorkItemLegacyType(class, subtype))
	}
}

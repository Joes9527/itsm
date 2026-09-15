package ent

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTicketUpdateBuildersDoNotExposeImmutableFieldSetters(t *testing.T) {
	for _, builder := range []any{(*TicketUpdate)(nil), (*TicketUpdateOne)(nil)} {
		builderType := reflect.TypeOf(builder)
		for _, setter := range []string{"SetTicketNumber", "SetRecordClass"} {
			_, exists := builderType.MethodByName(setter)
			require.Falsef(t, exists, "%s must not expose %s", builderType, setter)
		}
	}
}

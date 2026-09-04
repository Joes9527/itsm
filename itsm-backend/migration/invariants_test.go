package migration

import (
	"testing"

	"ariga.io/atlas/sql/postgres"
	atlasschema "ariga.io/atlas/sql/schema"
	"github.com/stretchr/testify/require"
)

func TestVerifiedBaselineEntChangeAllowsOnlyKnownAtlasPredicateNormalization(t *testing.T) {
	index := func(predicate string) *atlasschema.Index {
		return &atlasschema.Index{
			Name:  "workitemrelation_tenant_id_source_work_item_id",
			Attrs: []atlasschema.Attr{&postgres.IndexPredicate{P: predicate}},
		}
	}
	known := &atlasschema.ModifyIndex{
		From:   index("((deleted_at IS NULL) AND ((relation_type)::text = 'investigated_by'::text))"),
		To:     index("deleted_at IS NULL AND relation_type = 'investigated_by'"),
		Change: atlasschema.ChangeAttr,
	}
	require.True(t, isVerifiedBaselineEntChange("work_item_relations", known))

	drifted := *known
	drifted.From = index("deleted_at IS NULL OR relation_type = 'investigated_by'")
	require.False(t, isVerifiedBaselineEntChange("work_item_relations", &drifted))
	require.False(t, isVerifiedBaselineEntChange("ticket_ccs", known))
}

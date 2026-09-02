package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// IntakeResolutionSnapshot freezes the authoritative resolution used to create
// a WorkItem so later configuration changes cannot rewrite creation history.
type IntakeResolutionSnapshot struct {
	ent.Schema
}

// Fields of the IntakeResolutionSnapshot.
func (IntakeResolutionSnapshot) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").Immutable().Positive(),
		field.Int("intake_request_id").Immutable().Positive(),
		field.Int("work_item_id").Immutable().Positive(),
		field.String("channel").Immutable().NotEmpty(),
		field.String("source_provider").Immutable(),
		field.String("source_event_id").Optional().Sensitive().Immutable(),
		field.String("source_conversation_id").Optional().Sensitive().Immutable(),
		field.Int("catalog_item_id").Optional().Nillable().Positive().Immutable(),
		field.String("catalog_version").Optional().Immutable(),
		field.String("record_class").Immutable().NotEmpty(),
		field.JSON("cti_snapshot", json.RawMessage{}).Optional().Immutable(),
		field.JSON("ci_ids", []int{}).Default([]int{}).Immutable(),
		field.String("form_schema_version").Optional().Immutable(),
		field.Int("workflow_definition_id").Optional().Nillable().Positive().Immutable(),
		field.String("workflow_definition_key").Optional().Immutable(),
		field.String("workflow_definition_version").Optional().Immutable(),
		field.Bool("no_process").Default(false).Immutable(),
		field.Int("sla_definition_id").Optional().Nillable().Positive().Immutable(),
		field.String("resolver_version").Immutable().NotEmpty(),
		field.String("request_digest").Immutable().NotEmpty().Sensitive(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

// Indexes of the IntakeResolutionSnapshot.
func (IntakeResolutionSnapshot) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("intake_request_id").Unique(),
		index.Fields("work_item_id").Unique(),
		index.Fields("tenant_id", "created_at"),
	}
}

package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// KafTaskActionLedger is the durable, tenant-scoped idempotency coordinator
// for one KAF action execution scope.
type KafTaskActionLedger struct {
	ent.Schema
}

// Fields of the KafTaskActionLedger.
func (KafTaskActionLedger) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").Immutable().Positive(),
		field.String("task_id").Immutable().NotEmpty(),
		field.String("run_id").Immutable().NotEmpty(),
		field.String("step_id").Immutable().NotEmpty(),
		field.String("action").Immutable().NotEmpty(),
		field.String("idempotency_key").Immutable().NotEmpty(),
		field.String("correlation_id").Immutable().NotEmpty(),
		field.String("procedure_ref").Immutable().NotEmpty(),
		field.String("procedure_version").Immutable().NotEmpty(),
		field.String("result_status").Default("pending"),
		field.JSON("result_payload", json.RawMessage{}).Optional(),
		field.String("lease_owner").Optional().Sensitive(),
		field.Time("lease_expires_at").Optional(),
		field.String("last_error_code").Optional(),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Indexes of the KafTaskActionLedger.
func (KafTaskActionLedger) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "task_id", "run_id", "step_id").Unique(),
		index.Fields("tenant_id", "idempotency_key").Unique(),
	}
}

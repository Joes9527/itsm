package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// OutboxEvent records an event that must be delivered after its domain
// transaction commits.
type OutboxEvent struct {
	ent.Schema
}

// Fields of the OutboxEvent.
func (OutboxEvent) Fields() []ent.Field {
	return []ent.Field{
		field.String("event_id").
			Comment("Immutable cross-system event identifier").
			Unique().
			Immutable().
			NotEmpty(),
		field.String("event_type").
			Comment("Event type consumed by the delivery target").
			NotEmpty(),
		field.Int("tenant_id").
			Comment("Tenant that owns the event").
			Immutable().
			Positive(),
		field.String("aggregate_type").
			Comment("Owning aggregate type").
			NotEmpty(),
		field.String("aggregate_id").
			Comment("Owning aggregate identifier").
			NotEmpty(),
		field.JSON("payload", json.RawMessage{}).
			Comment("Serialized event payload").
			Sensitive(),
		field.String("status").
			Comment("Delivery status: pending, publishing, published, dead").
			Default("pending"),
		field.Int("attempt_count").
			Comment("Number of failed delivery attempts").
			Default(0),
		field.Time("next_attempt_at").
			Comment("Earliest time this event may be claimed for delivery").
			Default(time.Now),
		field.String("claim_token").
			Comment("Opaque identity for the active publishing lease").
			Optional().
			Sensitive(),
		field.Time("claim_expires_at").
			Comment("Expiry of the active publishing lease").
			Optional(),
		field.Time("published_at").
			Comment("Time at which the event was successfully delivered").
			Optional(),
		field.Text("last_error").
			Comment("Last delivery failure summary").
			Optional().
			Sensitive(),
		field.Time("created_at").
			Default(time.Now),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Indexes of the OutboxEvent.
func (OutboxEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("event_id").Unique(),
		index.Fields("tenant_id", "status", "next_attempt_at"),
		index.Fields("status", "claim_expires_at"),
	}
}

package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// WorkItemRelation holds the schema definition for structured cross-domain
// relationships between WorkItems (Incident/Problem/Change/ServiceRequestItem/
// CatalogTask, all physically rows in the tickets table). See
// docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md §10.
//
// This schema intentionally has no ent edges to Ticket — source/target are
// plain int columns joined at the application layer, matching how
// ProcessTask.process_instance_id already inherits identity by plain FK
// rather than a required ent edge elsewhere in this codebase. Keeping this
// table decoupled from Ticket's own Edges() means Wave 2 domain migrations
// don't need to touch ticket.go a second time just to add relation traversal.
type WorkItemRelation struct {
	ent.Schema
}

// Fields of the WorkItemRelation.
func (WorkItemRelation) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").
			Comment("租户ID").
			Positive(),
		field.Int("source_work_item_id").
			Comment("源 WorkItem（tickets.id）").
			Positive(),
		field.Int("target_work_item_id").
			Comment("目标 WorkItem（tickets.id）").
			Positive(),
		field.String("relation_type").
			Comment("关系类型：investigated_by/caused_by/resolved_by_change/requested_change/fulfilled_by/parent_child/duplicate_of/related_to").
			NotEmpty(),
		field.Int("created_by_id").
			Comment("创建人ID").
			Positive(),
		field.JSON("metadata", map[string]interface{}{}).
			Comment("少量关系专属元数据，不存业务主体").
			Optional(),
		field.Time("created_at").
			Comment("创建时间").
			Default(time.Now),
		field.Time("deleted_at").
			Comment("软删除时间").
			Optional().
			Nillable(),
	}
}

// Indexes of the WorkItemRelation.
func (WorkItemRelation) Indexes() []ent.Index {
	return []ent.Index{
		// Only live duplicate relation tuples are rejected; soft-deleted rows do
		// not prevent relinking the same WorkItems.
		//
		// entsql.IndexWhere generates a partial index in both SQLite and PostgreSQL.
		index.Fields("tenant_id", "source_work_item_id", "target_work_item_id", "relation_type").
			Unique().
			Annotations(entsql.IndexWhere("deleted_at IS NULL")),
		// An incident can have only one active investigated_by Problem relation.
		// Soft-deleted relations do not participate so a replacement may be linked.
		index.Fields("tenant_id", "source_work_item_id").
			Unique().
			Annotations(entsql.IndexWhere("deleted_at IS NULL AND relation_type = 'investigated_by'")),
		index.Fields("tenant_id", "target_work_item_id"),
	}
}

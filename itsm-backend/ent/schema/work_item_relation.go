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
		// 部分唯一索引：只约束"活着"的关系行。deleted_at 是软删除标记，若唯一索引
		// 覆盖已软删除的行，则解除关联后再重新关联同一对 WorkItem + 同一 relation_type
		// 会永久撞唯一约束——第一次软删除之后这条关系就再也建不回来了。
		//
		// entsql.IndexWhere 在 SQLite 和 PostgreSQL 上都生成部分索引（ent v0.14.6），
		// 正好覆盖本项目的测试库和生产库，所以不需要退化成硬删除。
		// WHERE 子句要跟数据库里存储的规范形式一致（见 entsql.IndexWhere 的文档注释），
		// 因此写成大写的 "deleted_at IS NULL"。
		index.Fields("tenant_id", "source_work_item_id", "target_work_item_id", "relation_type").
			Unique().
			Annotations(entsql.IndexWhere("deleted_at IS NULL")),
		index.Fields("tenant_id", "target_work_item_id"),
	}
}

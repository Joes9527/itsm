package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"itsm-backend/handlers/common/accessgrant"
)

// CatalogAccessPolicy is declared access configuration, not a tool registry.
type CatalogAccessPolicy struct{ ent.Schema }

// PostgreSQL CHECKs and their SQL function dependency are owned by the
// mandatory canonical migration.ReconcileSchemaInvariants phase after Ent.
// Do not rely on historical migration 030 being replayed at startup.
func (CatalogAccessPolicy) Fields() []ent.Field {
	return []ent.Field{
		field.Int("catalog_id").Positive().Unique().Immutable(),
		field.Int("version").Positive().Default(1),
		field.Enum("provider").Values("graph"),
		field.String("external_system").NotEmpty(),
		field.String("group_id").NotEmpty(),
		field.String("duration_field").NotEmpty(),
		field.JSON("duration_options", []accessgrant.DurationOption{}),
	}
}

func (CatalogAccessPolicy) Edges() []ent.Edge {
	return []ent.Edge{edge.To("catalog", ServiceCatalog.Type).Field("catalog_id").Required().Unique().Immutable()}
}

func (CatalogAccessPolicy) Indexes() []ent.Index {
	return []ent.Index{index.Fields("catalog_id").Unique()}
}

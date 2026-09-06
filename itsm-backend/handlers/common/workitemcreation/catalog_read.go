package workitemcreation

// CatalogReadDefinition is the catalog owner's authorized display projection.
// It contains no storage identity, provider secrets or authorization bypass.
type CatalogReadDefinition struct {
	ID                                                                int
	Name, Description, TargetClass, CatalogVersion, FormSchemaVersion string
	Fields                                                            []CatalogReadField
}
type CatalogReadField struct {
	Name, Label, FieldType string
	Required               bool
	Options                []any
}

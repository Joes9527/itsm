// Package relationmetadata defines relation-only storage types without domain
// dependencies so Ent can use them without an import cycle.
package relationmetadata

// Metadata contains relation policy, never duplicated IDs or professional state.
type Metadata struct {
	Required bool `json:"required"`
}

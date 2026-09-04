package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	entmigrate "itsm-backend/ent/migrate"
	"itsm-backend/pkg/seeder"

	entschema "entgo.io/ent/dialect/sql/schema"
)

const (
	currentReleaseID       = "itsm-v1.1"
	currentSchemaVersion   = "028_schema_release_state"
	currentBaselineVersion = "2026-09-04"
)

// Ent's migration planner links and annotates its generated table descriptors
// while it runs. Capture the compiled artifact identity before any planner can
// mutate those package globals so release identity remains stable for re-entry.
var compiledEntSchemaFingerprint = mustEntSchemaFingerprint(entmigrate.Tables)

// ReleaseManifest is the complete deterministic identity of one database release.
type ReleaseManifest struct {
	ReleaseID            string          `json:"releaseId"`
	SchemaVersion        string          `json:"schemaVersion"`
	BaselineVersion      string          `json:"baselineVersion"`
	EntSchemaFingerprint string          `json:"entSchemaFingerprint"`
	Assets               []ReleaseAsset  `json:"assets"`
	SeedComponents       []SeedComponent `json:"seedComponents"`
}

// ReleaseAsset identifies an immutable release asset by logical name and content digest.
type ReleaseAsset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// SeedComponent identifies one production seed component and its template version.
type SeedComponent struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// CurrentRelease builds the release identity exclusively from data available in
// the compiled artifact. It never reads source files or build-machine metadata.
func CurrentRelease() ReleaseManifest {
	entry, err := CurrentReleaseCatalogEntry()
	if err != nil {
		panic(fmt.Sprintf("load current release catalog entry: %v", err))
	}
	assets := make([]ReleaseAsset, 0, len(RegisteredMigrations)+2+len(entry.TransitionAssets))
	for _, migration := range RegisteredMigrations {
		assets = append(assets, ReleaseAsset{
			Name:   migration.Version,
			SHA256: checksumSQL(GetMigrationSQL(migration.Version)),
		})
	}
	assets = append(assets, ReleaseAsset{
		Name:   CurrentBaselineAssetName,
		SHA256: checksumSQL(CurrentBaselineSQL()),
	})
	assets = append(assets, currentSourceSchemaAsset())
	assets = append(assets, entry.TransitionAssets...)
	components := make([]SeedComponent, 0, len(seeder.ProductionComponentNames))
	for _, name := range seeder.ProductionComponentNames {
		components = append(components, SeedComponent{
			Name:    name,
			Version: seeder.CurrentTenantTemplateVersion,
		})
	}
	return ReleaseManifest{
		ReleaseID:            currentReleaseID,
		SchemaVersion:        currentSchemaVersion,
		BaselineVersion:      currentBaselineVersion,
		EntSchemaFingerprint: compiledEntSchemaFingerprint,
		Assets:               assets,
		SeedComponents:       components,
	}
}

// Checksum returns the SHA-256 of canonical JSON. Collection order supplied by
// callers is not significant; identity and content are.
func (manifest ReleaseManifest) Checksum() (string, error) {
	canonical, err := canonicalReleaseManifest(manifest)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode release manifest: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalReleaseManifest(manifest ReleaseManifest) (ReleaseManifest, error) {
	if strings.TrimSpace(manifest.ReleaseID) == "" || strings.TrimSpace(manifest.SchemaVersion) == "" ||
		strings.TrimSpace(manifest.BaselineVersion) == "" {
		return ReleaseManifest{}, fmt.Errorf("release manifest identity is incomplete")
	}
	if !sha256Pattern.MatchString(manifest.EntSchemaFingerprint) {
		return ReleaseManifest{}, fmt.Errorf("release manifest Ent schema fingerprint is invalid")
	}

	canonical := manifest
	canonical.Assets = append([]ReleaseAsset(nil), manifest.Assets...)
	if len(canonical.Assets) == 0 {
		return ReleaseManifest{}, fmt.Errorf("release manifest assets are required")
	}
	sort.Slice(canonical.Assets, func(i, j int) bool {
		return canonical.Assets[i].Name < canonical.Assets[j].Name
	})
	for index, asset := range canonical.Assets {
		if strings.TrimSpace(asset.Name) == "" || !sha256Pattern.MatchString(asset.SHA256) {
			return ReleaseManifest{}, fmt.Errorf("release manifest asset is invalid")
		}
		if index > 0 && canonical.Assets[index-1].Name == asset.Name {
			return ReleaseManifest{}, fmt.Errorf("release manifest contains duplicate asset")
		}
	}

	canonical.SeedComponents = append([]SeedComponent(nil), manifest.SeedComponents...)
	if len(canonical.SeedComponents) == 0 {
		return ReleaseManifest{}, fmt.Errorf("release manifest seed components are required")
	}
	sort.Slice(canonical.SeedComponents, func(i, j int) bool {
		return canonical.SeedComponents[i].Name < canonical.SeedComponents[j].Name
	})
	for index, component := range canonical.SeedComponents {
		if strings.TrimSpace(component.Name) == "" || strings.TrimSpace(component.Version) == "" {
			return ReleaseManifest{}, fmt.Errorf("release manifest seed component is invalid")
		}
		if index > 0 && canonical.SeedComponents[index-1].Name == component.Name {
			return ReleaseManifest{}, fmt.Errorf("release manifest contains duplicate seed component")
		}
	}
	return canonical, nil
}

type canonicalEntSchema struct {
	Tables []canonicalEntTable `json:"tables"`
}

type canonicalEntTable struct {
	Name        string                `json:"name"`
	Schema      string                `json:"schema,omitempty"`
	Columns     []canonicalEntColumn  `json:"columns"`
	PrimaryKey  []string              `json:"primaryKey,omitempty"`
	Indexes     []canonicalEntIndex   `json:"indexes,omitempty"`
	ForeignKeys []canonicalEntForeign `json:"foreignKeys,omitempty"`
	Annotation  any                   `json:"annotation,omitempty"`
	Comment     string                `json:"comment,omitempty"`
	View        bool                  `json:"view,omitempty"`
}

type canonicalEntColumn struct {
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	SchemaType []canonicalMapItem `json:"schemaType,omitempty"`
	Attr       string             `json:"attr,omitempty"`
	Size       int64              `json:"size,omitempty"`
	Key        string             `json:"key,omitempty"`
	Unique     bool               `json:"unique,omitempty"`
	Increment  bool               `json:"increment,omitempty"`
	Nullable   bool               `json:"nullable,omitempty"`
	Default    any                `json:"default,omitempty"`
	Enums      []string           `json:"enums,omitempty"`
	Collation  string             `json:"collation,omitempty"`
	Comment    string             `json:"comment,omitempty"`
}

type canonicalMapItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type canonicalEntIndex struct {
	Name       string   `json:"name"`
	Unique     bool     `json:"unique,omitempty"`
	Columns    []string `json:"columns"`
	Annotation any      `json:"annotation,omitempty"`
}

type canonicalEntForeign struct {
	Symbol     string   `json:"symbol"`
	Columns    []string `json:"columns"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
	OnUpdate   string   `json:"onUpdate,omitempty"`
	OnDelete   string   `json:"onDelete,omitempty"`
}

func mustEntSchemaFingerprint(tables []*entschema.Table) string {
	fingerprint, err := entSchemaFingerprint(tables)
	if err != nil {
		panic(err)
	}
	return fingerprint
}

func entSchemaFingerprint(tables []*entschema.Table) (string, error) {
	document := canonicalEntSchema{Tables: make([]canonicalEntTable, 0, len(tables))}
	for _, table := range tables {
		if table == nil {
			return "", fmt.Errorf("fingerprint Ent schema: nil table")
		}
		canonical := canonicalEntTable{
			Name:       table.Name,
			Schema:     table.Schema,
			Annotation: table.Annotation,
			Comment:    table.Comment,
			View:       table.View,
		}
		for _, column := range table.Columns {
			if column == nil {
				return "", fmt.Errorf("fingerprint Ent schema: nil column")
			}
			canonical.Columns = append(canonical.Columns, canonicalEntColumn{
				Name:       column.Name,
				Type:       column.Type.String(),
				SchemaType: canonicalStringMap(column.SchemaType),
				Attr:       column.Attr,
				Size:       column.Size,
				Key:        column.Key,
				Unique:     column.Unique,
				Increment:  column.Increment,
				Nullable:   column.Nullable,
				Default:    column.Default,
				Enums:      append([]string(nil), column.Enums...),
				Collation:  column.Collation,
				Comment:    column.Comment,
			})
		}
		sort.Slice(canonical.Columns, func(i, j int) bool { return canonical.Columns[i].Name < canonical.Columns[j].Name })
		canonical.PrimaryKey = columnNames(table.PrimaryKey)
		for _, index := range table.Indexes {
			if index == nil {
				return "", fmt.Errorf("fingerprint Ent schema: nil index")
			}
			canonical.Indexes = append(canonical.Indexes, canonicalEntIndex{
				Name:       index.Name,
				Unique:     index.Unique,
				Columns:    columnNames(index.Columns),
				Annotation: index.Annotation,
			})
		}
		sort.Slice(canonical.Indexes, func(i, j int) bool { return canonical.Indexes[i].Name < canonical.Indexes[j].Name })
		for _, foreignKey := range table.ForeignKeys {
			if foreignKey == nil || foreignKey.RefTable == nil {
				return "", fmt.Errorf("fingerprint Ent schema: incomplete foreign key")
			}
			canonical.ForeignKeys = append(canonical.ForeignKeys, canonicalEntForeign{
				Symbol:     foreignKey.Symbol,
				Columns:    columnNames(foreignKey.Columns),
				RefTable:   foreignKey.RefTable.Name,
				RefColumns: columnNames(foreignKey.RefColumns),
				OnUpdate:   string(foreignKey.OnUpdate),
				OnDelete:   string(foreignKey.OnDelete),
			})
		}
		sort.Slice(canonical.ForeignKeys, func(i, j int) bool {
			return canonical.ForeignKeys[i].Symbol < canonical.ForeignKeys[j].Symbol
		})
		document.Tables = append(document.Tables, canonical)
	}
	sort.Slice(document.Tables, func(i, j int) bool { return document.Tables[i].Name < document.Tables[j].Name })
	payload, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("fingerprint Ent schema: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalStringMap(values map[string]string) []canonicalMapItem {
	result := make([]canonicalMapItem, 0, len(values))
	for key, value := range values {
		result = append(result, canonicalMapItem{Key: key, Value: value})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

func columnNames(columns []*entschema.Column) []string {
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		if column != nil {
			names = append(names, column.Name)
		}
	}
	return names
}

package service_catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"itsm-backend/ent"
	"itsm-backend/ent/catalogaccesspolicy"
	"itsm-backend/ent/fielddefinition"
	"itsm-backend/ent/servicecatalog"
	"itsm-backend/handlers/common/accessgrant"
	"itsm-backend/service"
	"math"
	"strconv"
	"strings"
	"time"
)

func ValidateAccessPolicy(p *accessgrant.Policy, fields []service.FieldDefinitionInput) error {
	if p == nil || p.Provider != accessgrant.Graph || strings.TrimSpace(p.ExternalSystem) == "" || strings.TrimSpace(p.GroupID) == "" || p.DurationField == "" || len(p.DurationOptions) == 0 {
		return fmt.Errorf("access policy requires supported provider, trusted external target and finite durations")
	}
	var field *service.FieldDefinitionInput
	for i := range fields {
		if fields[i].Name == p.DurationField {
			field = &fields[i]
			break
		}
	}
	if field == nil || field.FieldType != "select" || !field.Required || len(field.Options) != len(p.DurationOptions) {
		return fmt.Errorf("access duration requires a required choice field with the same options")
	}
	// FieldDefinition owns choice values and opaque transport keys. Policy keys are
	// original named values; Intake resolves its opaque keys before SR preparation.
	if _, err := service.ProjectCatalogOptions(field.Options); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, option := range p.DurationOptions {
		if strings.TrimSpace(option.Key) == "" || strings.TrimSpace(option.Label) == "" || seen[option.Key] || option.Seconds <= 0 || option.Seconds > math.MaxInt64/int64(time.Second) {
			return fmt.Errorf("access duration must be unique, named and finite")
		}
		seen[option.Key] = true
		found := false
		for _, raw := range field.Options {
			candidate, ok := raw.(map[string]any)
			if ok && candidate["value"] == option.Key && candidate["label"] == option.Label {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("access duration option does not match its field definition")
		}
	}
	return nil
}

func ReadAccessPolicy(ctx context.Context, client *ent.Client, tenantID, catalogID int) (*accessgrant.Policy, error) {
	row, err := client.CatalogAccessPolicy.Query().Where(catalogaccesspolicy.CatalogIDEQ(catalogID), catalogaccesspolicy.HasCatalogWith(servicecatalog.TenantIDEQ(tenantID))).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &accessgrant.Policy{ID: row.ID, Version: row.Version, Provider: accessgrant.Provider(row.Provider), ExternalSystem: row.ExternalSystem, GroupID: row.GroupID, DurationField: row.DurationField, DurationOptions: row.DurationOptions}, nil
}
func saveAccessPolicy(ctx context.Context, tx *ent.Tx, tenantID, catalogID int, p *accessgrant.Policy) error {
	if p == nil {
		return nil
	}
	existing, err := ReadAccessPolicy(ctx, tx.Client(), tenantID, catalogID)
	if err != nil {
		return err
	}
	if existing == nil {
		_, err = tx.CatalogAccessPolicy.Create().SetCatalogID(catalogID).SetProvider(catalogaccesspolicy.Provider(p.Provider)).SetExternalSystem(p.ExternalSystem).SetGroupID(p.GroupID).SetDurationField(p.DurationField).SetDurationOptions(p.DurationOptions).Save(ctx)
	} else {
		_, err = tx.CatalogAccessPolicy.UpdateOneID(existing.ID).AddVersion(1).SetProvider(catalogaccesspolicy.Provider(p.Provider)).SetExternalSystem(p.ExternalSystem).SetGroupID(p.GroupID).SetDurationField(p.DurationField).SetDurationOptions(p.DurationOptions).Save(ctx)
	}
	return err
}

// PublicationConfiguration is reached through the existing registered KAF
// capability owner. A trusted configuration reference is the policy row ID.
func (s *Service) PublicationConfiguration(ctx context.Context, client *ent.Client, tenantID int, action, ref string) (json.RawMessage, error) {
	if action != accessgrant.Capability {
		return json.Marshal(map[string]any{"action": action, "ref": ref})
	}
	id, err := strconv.Atoi(ref)
	if err != nil || id <= 0 {
		return json.Marshal(nil)
	}
	row, err := client.CatalogAccessPolicy.Query().Where(catalogaccesspolicy.IDEQ(id), catalogaccesspolicy.HasCatalogWith(servicecatalog.TenantIDEQ(tenantID))).Only(ctx)
	if ent.IsNotFound(err) {
		return json.Marshal(nil)
	}
	if err != nil {
		return nil, err
	}
	p, err := ReadAccessPolicy(ctx, client, tenantID, row.CatalogID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(p)
}
func (s *Service) ValidatePublicationConfiguration(ctx context.Context, client *ent.Client, tenantID int, action, ref string) error {
	if action != accessgrant.Capability {
		return fmt.Errorf("unsupported external grant capability")
	}
	raw, err := s.PublicationConfiguration(ctx, client, tenantID, action, ref)
	if err != nil {
		return err
	}
	var p *accessgrant.Policy
	if err = json.Unmarshal(raw, &p); err != nil || p == nil {
		return fmt.Errorf("external grant policy is unavailable")
	}
	row, err := client.CatalogAccessPolicy.Get(ctx, p.ID)
	if err != nil {
		return err
	}
	fields, err := client.FieldDefinition.Query().Where(fielddefinition.TenantIDEQ(tenantID), fielddefinition.EntityTypeEQ("service_catalog"), fielddefinition.EntityIDEQ(row.CatalogID), fielddefinition.IsActiveEQ(true)).All(ctx)
	if err != nil {
		return err
	}
	return ValidateAccessPolicy(p, toFieldDefinitionInputsFromEnt(fields))
}

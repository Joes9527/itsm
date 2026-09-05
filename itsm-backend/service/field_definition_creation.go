package service

import (
	"context"
	"fmt"
	"itsm-backend/ent"
	"itsm-backend/ent/fielddefinition"
	creation "itsm-backend/handlers/common/workitemcreation"
	"reflect"
	"strings"
	"time"
)

func (*FieldDefinitionService) ValidateCreationValues(ctx context.Context, tx *ent.Tx, tenantID int, scope string, scopeID int, values map[string]any) error {
	definitions, err := tx.FieldDefinition.Query().Where(fielddefinition.TenantIDEQ(tenantID), fielddefinition.EntityTypeEQ(scope), fielddefinition.EntityIDEQ(scopeID), fielddefinition.IsActiveEQ(true)).All(ctx)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not load creation fields", err)
	}
	known := map[string]bool{}
	for _, definition := range definitions {
		known[definition.Name] = true
		value, present := values[definition.Name]
		missing := !present || value == nil
		if str, ok := value.(string); ok && strings.TrimSpace(str) == "" {
			missing = true
		}
		if value != nil {
			v := reflect.ValueOf(value)
			if (v.Kind() == reflect.Slice || v.Kind() == reflect.Map) && v.Len() == 0 {
				missing = true
			}
		}
		if definition.Required && missing {
			return creation.NewDomainValidationFailed("required dynamic field is missing", nil, creation.FieldError{Field: "formValues." + definition.Name, Message: "required"})
		}
		if missing {
			continue
		}
		if err := validateFieldValue(definition, value); err != nil {
			return creation.NewDomainValidationFailed("invalid dynamic field", err, creation.FieldError{Field: "formValues." + definition.Name, Message: err.Error()})
		}
	}
	for key := range values {
		if !known[key] {
			return creation.NewDomainValidationFailed("unknown dynamic field", nil, creation.FieldError{Field: "formValues." + key, Message: fmt.Sprintf("not defined by %s", scope)})
		}
	}
	return nil
}

func (*FieldDefinitionService) CreationFieldScope(ctx context.Context, tx *ent.Tx, tenantID int, scope string, scopeID int) (*creation.FieldDefinitionScope, error) {
	definitions, err := tx.FieldDefinition.Query().Where(fielddefinition.TenantIDEQ(tenantID), fielddefinition.EntityTypeEQ(scope), fielddefinition.EntityIDEQ(scopeID), fielddefinition.IsActiveEQ(true)).Order(ent.Asc(fielddefinition.FieldSortOrder), ent.Asc(fielddefinition.FieldID)).All(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not load field revision", err)
	}
	for _, definition := range definitions {
		definition.CreatedAt = time.Time{}
		definition.UpdatedAt = time.Time{}
	}
	version, err := creation.ConfigurationRevision("form-v1", definitions)
	if err != nil {
		return nil, err
	}
	return &creation.FieldDefinitionScope{EntityType: scope, EntityID: scopeID, Version: version}, nil
}

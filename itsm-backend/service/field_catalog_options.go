package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	creation "itsm-backend/handlers/common/workitemcreation"
)

// Catalog option keys are opaque transport identifiers, not submitted values.
// JSON distinguishes numbers from strings and retains json.Number lexemes.
// No key/value shadow state is persisted: the current confirmed definition is
// the sole authority for resolving a key back to its original JSON value.
func catalogOptionKey(value any) (string, []byte, error) {
	if value == nil {
		return "", nil, creation.NewDomainValidationFailed("catalog option requires a value", nil)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", nil, creation.NewDomainValidationFailed("invalid catalog option value", err)
	}
	return "option:v1:" + base64.RawURLEncoding.EncodeToString(raw), raw, nil
}
func ProjectCatalogOptions(options []any) ([]creation.CatalogReadOption, error) {
	result := make([]creation.CatalogReadOption, 0, len(options))
	for _, raw := range options {
		option, ok := raw.(map[string]any)
		if !ok {
			return nil, creation.NewDomainValidationFailed("invalid catalog option", nil)
		}
		label, ok := option["label"].(string)
		if !ok || label == "" {
			return nil, creation.NewDomainValidationFailed("invalid catalog option label", nil)
		}
		key, _, err := catalogOptionKey(option["value"])
		if err != nil {
			return nil, err
		}
		result = append(result, creation.CatalogReadOption{Key: key, Label: label})
	}
	return result, nil
}

// ResolveCatalogOptionKeys is called only for a trusted Intake key adapter,
// after receipt replay and catalog/form revision checks. It detaches every
// nested submitted value, preserving the original wire digest and retry input.
// Ordinary native adapters continue to submit original field values.
func ResolveCatalogOptionKeys(definitions []creation.ResolvedFieldDefinition, values map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(values)
	if err != nil {
		return nil, creation.NewDomainValidationFailed("invalid catalog values", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result map[string]any
	if err = decoder.Decode(&result); err != nil {
		return nil, creation.NewDomainValidationFailed("invalid catalog values", err)
	}
	for _, definition := range definitions {
		value, present := result[definition.Key]
		if !present || value == nil || (definition.DataType != "select" && definition.DataType != "multiselect") {
			continue
		}
		invalid := func() error {
			return creation.NewDomainValidationFailed("unknown catalog option key", nil, creation.FieldError{Field: "formValues." + definition.Key, Message: "select a configured option key"})
		}
		resolve := func(submitted any) (any, error) {
			key, ok := submitted.(string)
			if !ok {
				return nil, invalid()
			}
			for _, rawOption := range definition.Options {
				option, ok := rawOption.(map[string]any)
				if !ok {
					return nil, creation.NewDomainValidationFailed("invalid catalog option", nil)
				}
				candidate, original, err := catalogOptionKey(option["value"])
				if err != nil {
					return nil, err
				}
				if candidate == key {
					d := json.NewDecoder(bytes.NewReader(original))
					d.UseNumber()
					var value any
					if err = d.Decode(&value); err != nil {
						return nil, creation.NewDomainValidationFailed("invalid catalog option", err)
					}
					return value, nil
				}
			}
			return nil, invalid()
		}
		if definition.DataType == "select" {
			resolved, err := resolve(value)
			if err != nil {
				return nil, err
			}
			result[definition.Key] = resolved
			continue
		}
		submitted, ok := value.([]any)
		if !ok {
			return nil, invalid()
		}
		resolved := make([]any, 0, len(submitted))
		for _, key := range submitted {
			value, err := resolve(key)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, value)
		}
		result[definition.Key] = resolved
	}
	return result, nil
}

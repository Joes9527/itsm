package service_catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"itsm-backend/service"
	"strings"
)

func catalogFieldInputs(fields []map[string]interface{}) ([]service.FieldDefinitionInput, error) {
	result := make([]service.FieldDefinitionInput, 0, len(fields))
	for i, field := range fields {
		var value struct {
			Name      string        `json:"name"`
			Label     string        `json:"label"`
			Type      string        `json:"type"`
			Required  bool          `json:"required"`
			Options   []interface{} `json:"options"`
			SortOrder *int          `json:"sortOrder"`
		}
		raw, err := json.Marshal(field)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("field %d: %w", i, err)
		}
		if strings.TrimSpace(value.Name) == "" || strings.TrimSpace(value.Label) == "" {
			return nil, fmt.Errorf("field %d requires name and label", i)
		}
		order := i
		if value.SortOrder != nil {
			order = *value.SortOrder
		}
		if order < 0 {
			return nil, fmt.Errorf("field %d sortOrder cannot be negative", i)
		}
		result = append(result, service.FieldDefinitionInput{Name: value.Name, Label: value.Label, FieldType: value.Type, Required: value.Required, Options: value.Options, SortOrder: order})
	}
	return result, nil
}

package service

import (
	"encoding/json"
	"reflect"
	"strings"

	"itsm-backend/common"
	creation "itsm-backend/handlers/common/workitemcreation"
)

type incidentSourceBinding struct {
	channel, provider, defaultSource string
	sources                          []string
}
type incidentSourcePolicy struct {
	bindings map[[2]string]incidentSourceBinding
}

// Construction copies private policy data. Duplicate bindings are configuration
// errors; no provider names or connector categories are inferred at runtime.
func newIncidentSourcePolicy(bindings ...incidentSourceBinding) *incidentSourcePolicy {
	p := &incidentSourcePolicy{bindings: make(map[[2]string]incidentSourceBinding, len(bindings))}
	for _, binding := range bindings {
		key := [2]string{binding.channel, binding.provider}
		if _, exists := p.bindings[key]; exists {
			panic("duplicate incident source binding")
		}
		binding.sources = append([]string(nil), binding.sources...)
		p.bindings[key] = binding
	}
	return p
}

var incidentCreationSources = newIncidentSourcePolicy(
	incidentSourceBinding{channel: "http", defaultSource: "manual", sources: []string{"manual", "user"}},
	incidentSourceBinding{channel: "bpmn", provider: "bpmn", defaultSource: "system", sources: []string{"system"}},
)

func (*IncidentService) ValidateIncidentCreationInput(identity creation.Identity, ref *creation.SourceReference, input *creation.IncidentInput) (string, error) {
	binding, ok := incidentCreationSources.bindings[[2]string{identity.Channel, identity.Provider}]
	if !ok {
		return "", creation.NewPermissionDenied("incident creation source binding is not registered", nil)
	}
	if binding.provider == "" {
		if ref != nil {
			return "", creation.NewPermissionDenied("public incident creation cannot claim a source reference", nil)
		}
	} else if ref == nil || ref.Provider != binding.provider || strings.TrimSpace(ref.EventID) == "" {
		return "", creation.NewPermissionDenied("verified incident creation source reference is required", nil)
	}
	source := ""
	if input != nil {
		source = strings.TrimSpace(input.Source)
	}
	if source == "" {
		source = binding.defaultSource
	}
	switch source {
	case "manual", "user", "system", "monitoring":
	default:
		return "", creation.NewInvalidCommand("invalid incident source", creation.FieldError{Field: "incident.source", Message: "unsupported value"}, nil)
	}
	allowed := false
	for _, candidate := range binding.sources {
		if candidate == source {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", creation.NewPermissionDenied("incident source is not permitted for the authenticated binding", nil)
	}
	if input != nil {
		if err := validateIncidentMetadata(input.Metadata); err != nil {
			return "", err
		}
	}
	return source, nil
}

// Bound traversal before canonical JSON encoding, including typed Go maps/slices
// from internal adapters. Values are inspected without conversion or mutation.
func validateIncidentMetadata(metadata map[string]any) error {
	const maxDepth, maxEntries, maxBytes = 8, 1024, 65536
	invalid := func() error {
		return creation.NewInvalidCommand("invalid incident metadata", creation.FieldError{Field: "incident.metadata", Message: "must be credential-free JSON within 8 nesting levels, 1024 entries and 65536 bytes"}, nil)
	}
	entries, size := 0, 0
	var visit func(reflect.Value, int) bool
	visit = func(value reflect.Value, depth int) bool {
		if depth > maxDepth {
			return false
		}
		if value.IsValid() && value.Kind() == reflect.Interface {
			return visit(value.Elem(), depth)
		}
		entries++
		if entries > maxEntries {
			return false
		}
		if !value.IsValid() {
			return true
		}
		// Custom encoders must not replace inspected values with hidden keys.
		if value.CanInterface() {
			if _, custom := value.Interface().(json.Marshaler); custom {
				return false
			}
		}
		// encoding/json also uses pointer receivers on addressable slice elements.
		if value.CanAddr() && value.Addr().CanInterface() {
			if _, custom := value.Addr().Interface().(json.Marshaler); custom {
				return false
			}
		}
		switch value.Kind() {
		case reflect.Map:
			if value.Type().Key().Kind() != reflect.String {
				return false
			}
			if value.Len() > maxEntries-entries {
				return false
			}
			iter := value.MapRange()
			for iter.Next() {
				key := iter.Key().String()
				size += len(key)
				if size > maxBytes || common.IsCredentialKey(key) || !visit(iter.Value(), depth+1) {
					return false
				}
			}
		case reflect.Slice, reflect.Array:
			if value.Len() > maxEntries-entries {
				return false
			}
			for i := 0; i < value.Len(); i++ {
				if !visit(value.Index(i), depth+1) {
					return false
				}
			}
		case reflect.String:
			size += value.Len()
			if size > maxBytes {
				return false
			}
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		default:
			return false
		}
		return true
	}
	if !visit(reflect.ValueOf(metadata), 0) {
		return invalid()
	}
	raw, err := json.Marshal(metadata)
	if err != nil || len(raw) > maxBytes {
		return invalid()
	}
	return nil
}

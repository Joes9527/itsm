package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// MaxJSONBodyBytes bounds JSON request bodies at connector and intake boundaries.
const MaxJSONBodyBytes = 1 << 20

// MaxJSONDepth bounds nested objects and arrays, including vendor extension data.
const MaxJSONDepth = 64

// DecodeJSONObject accepts exactly one bounded object, rejects duplicate members
// recursively, and preserves numbers without rounding. It does not restrict keys
// or rewrite the original bytes used by signed protocols.
func DecodeJSONObject(raw []byte) (map[string]any, error) {
	if len(raw) > MaxJSONBodyBytes {
		return nil, fmt.Errorf("JSON body size limit exceeded")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, err := readJSONValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("exactly one JSON object is required")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("JSON object is required")
	}
	return object, nil
}

func readJSONValue(d *json.Decoder, depth int) (any, error) {
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	if depth >= MaxJSONDepth {
		return nil, fmt.Errorf("JSON nesting limit exceeded")
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("invalid member")
			}
			if _, exists := object[name]; exists {
				return nil, fmt.Errorf("duplicate JSON member")
			}
			value, err := readJSONValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		_, err = d.Token()
		return object, err
	case '[':
		values := []any{}
		for d.More() {
			value, err := readJSONValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		_, err = d.Token()
		return values, err
	default:
		return nil, fmt.Errorf("unexpected delimiter")
	}
}

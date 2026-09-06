// Package jsonvalue defines persistence codecs for exact workflow inputs.
package jsonvalue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// NumberMap retains JSON decimal lexemes across persisted workflow reloads.
// It is used only by fields whose consumers require exact numeric semantics.
type NumberMap map[string]any

func (m *NumberMap) UnmarshalJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("exactly one JSON value is required")
	}
	*m = value
	return nil
}

// NumberArray preserves exact option values using the same persistence codec.
type NumberArray []any

func (a *NumberArray) UnmarshalJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value []any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("exactly one JSON value is required")
	}
	*a = value
	return nil
}

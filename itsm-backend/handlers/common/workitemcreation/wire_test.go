package workitemcreation

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestStrictWireFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../../../docs/contracts/fixtures/intake-wire-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Body string
		Valid      bool
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			cmd, e := DecodeCreateWorkItemCommand(strings.NewReader(c.Body))
			structuralOnly := c.Name == "invalid generic id" || c.Name == "invalid change ci" || c.Name == "blank provider" || c.Name == "blank event"
			if !c.Valid && !structuralOnly && e == nil {
				t.Fatal("strict decoder accepted invalid typed wire")
			}
			if e == nil {
				_, _, e = CanonicalizeCommand(cmd)
			}
			if (e == nil) != c.Valid {
				t.Fatalf("valid=%v, error=%v", c.Valid, e)
			}
		})
	}
}
func TestExactProfessionalAndDynamicNumbers(t *testing.T) {
	for _, field := range []string{"revenueImpact", "serviceAvailability", "dynamic"} {
		t.Run(field, func(t *testing.T) {
			digests := []string{}
			for _, number := range []string{"9007199254740992", "9007199254740993"} {
				raw := `{"idempotencyKey":"n","intakeKind":"incident","recordClass":"incident","confirmation":"confirmed","title":"Numeric","incident":{"impactAnalysis":{"businessImpact":{"` + field + `":` + number + `}}}}`
				if field == "dynamic" {
					raw = `{"idempotencyKey":"n","intakeKind":"incident","recordClass":"incident","confirmation":"confirmed","title":"Numeric","formValues":{"amount":` + number + `}}`
				}
				c, e := DecodeCreateWorkItemCommand(strings.NewReader(raw))
				if e != nil {
					t.Fatal(e)
				}
				n, d, e := CanonicalizeCommand(c)
				if e != nil {
					t.Fatal(e)
				}
				encoded, e := json.Marshal(n)
				if e != nil || !strings.Contains(string(encoded), number) {
					t.Errorf("lost exact value %s: %s (%v)", number, encoded, e)
				}
				digests = append(digests, d)
			}
			if digests[0] == digests[1] {
				t.Fatal("distinct accepted numbers share digest")
			}
		})
	}
}
func TestUnicodeCharacterBoundaries(t *testing.T) {
	for field, limit := range map[string]int{"title": 500, "description": 20000, "idempotencyKey": 200} {
		for _, length := range []int{200, limit, limit + 1} {
			c := catalogCommand()
			value := strings.Repeat("中", length)
			switch field {
			case "title":
				c.Title = value
			case "description":
				c.Description = value
			case "idempotencyKey":
				c.IdempotencyKey = value
			}
			_, _, e := CanonicalizeCommand(c)
			if (e == nil) != (length <= limit) {
				t.Errorf("%s %d characters: %v", field, length, e)
			}
		}
	}
}

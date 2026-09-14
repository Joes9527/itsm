package dto

import "testing"

// C1: one canonical WorkItem business key at every BPMN boundary. The legacy
// vocabulary ("ticket", "change", "service_request") must be rejected outright so no
// runtime path can silently interpret two identities for the same record.
func TestWorkItemBusinessKey(t *testing.T) {
	got, err := WorkItemBusinessKey("change_request", 42)
	if err != nil || got != "change_request:42" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, old := range []string{"ticket", "change", "service_request", "unknown"} {
		if _, err := WorkItemBusinessKey(old, 42); err == nil {
			t.Fatalf("legacy class accepted: %s", old)
		}
	}
	if _, err := WorkItemBusinessKey("incident", 0); err == nil {
		t.Fatal("non-positive work item ID must be rejected")
	}
	if _, err := WorkItemBusinessKey("incident", -1); err == nil {
		t.Fatal("negative work item ID must be rejected")
	}
}

// Every record class that owns a WorkItem must round-trip, including catalog_task,
// whose identity is legitimate even though creation capability is still gated.
func TestWorkItemBusinessKeyRoundTripsEveryRecordClass(t *testing.T) {
	for _, class := range WorkItemRecordClasses() {
		key, err := WorkItemBusinessKey(class, 7)
		if err != nil {
			t.Fatalf("record class %s must produce a key: %v", class, err)
		}
		parsedClass, parsedID, err := ParseWorkItemBusinessKey(key)
		if err != nil {
			t.Fatalf("canonical key %q must parse: %v", key, err)
		}
		if parsedClass != class || parsedID != 7 {
			t.Fatalf("round trip mismatch for %s: got %s/%d", class, parsedClass, parsedID)
		}
	}
}

// Parsing fails closed: a retired Wave-1 key ("ticket:1", "change:1",
// "service_request:1") or a malformed key is never accepted as a WorkItem identity,
// so old instances cannot be re-interpreted by the new runtime.
func TestParseWorkItemBusinessKeyRejectsLegacyAndMalformedKeys(t *testing.T) {
	for _, bad := range []string{"ticket:1", "change:1", "service_request:1", "", "incident", "incident:", "incident:abc", "incident:0", "unknown:1"} {
		if _, _, err := ParseWorkItemBusinessKey(bad); err == nil {
			t.Fatalf("malformed or legacy key accepted: %q", bad)
		}
	}
	// Canonical keys must remain valid, including the Release-compatible classes.
	for _, good := range []string{"generic:1", "change_request:1", "service_request_item:1", "incident:1", "problem:1", "catalog_task:1"} {
		if _, _, err := ParseWorkItemBusinessKey(good); err != nil {
			t.Fatalf("canonical key %q must parse: %v", good, err)
		}
	}
}

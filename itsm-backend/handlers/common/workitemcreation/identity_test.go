package workitemcreation

import (
	"errors"
	"strings"
	"testing"
)

func TestIdentityAndErrorPolicy(t *testing.T) {
	c := catalogCommand()
	if err := (Identity{}).ValidateCommand(c); !errors.Is(err, ErrAuthenticationRequired) {
		t.Fatal(err)
	}
	i := Identity{TenantID: 1, ActorID: 2, RequesterID: 2, Role: "requester", Channel: "kaf_web", Provider: "kaf"}
	if err := i.ValidateCommand(c); err != nil {
		t.Fatal(err)
	}
	c.SourceReference = &SourceReference{Provider: "forged", EventID: "e"}
	if err := i.ValidateCommand(c); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal(err)
	}
	c.SourceReference.Provider = "kaf"
	if err := i.ValidateCommand(c); err != nil {
		t.Fatal(err)
	}
	for code, status := range map[ErrorCode]int{InvalidCommand: 400, AuthenticationRequired: 401, PermissionDenied: 403, ReferenceNotFound: 404, IdempotencyConflict: 409, CatalogVersionConflict: 409, UnsupportedRecordClass: 422, DomainValidationFailed: 422, WorkflowBindingRequired: 422, InfrastructureUnavailable: 503, InternalFailure: 500} {
		e := NewIntakeError(code, "public", errors.New("private-secret"))
		if e.HTTPStatus != status || e.Retryable != (status == 503) || strings.Contains(e.PublicJSON(), "private-secret") {
			t.Fatalf("bad error policy: %s", e.PublicJSON())
		}
	}
}

package intake

import (
	"context"
	"errors"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"testing"
)

type fixtureCreator struct{ class string }

func (c *fixtureCreator) RecordClass() string { return c.class }
func (c *fixtureCreator) Prepare(context.Context, *ent.Tx, creation.ResolvedIntake) (*creation.CreationPlan, error) {
	panic("not used")
}
func (c *fixtureCreator) CreateExtension(context.Context, *ent.Tx, *ent.Ticket, *creation.CreationPlan) (*creation.ProfessionalReference, error) {
	panic("not used")
}
func TestRegistryFailsClosed(t *testing.T) {
	r := NewCreatorRegistry()
	var typedNil *fixtureCreator
	for _, c := range []creation.ProfessionalCreator{nil, typedNil, &fixtureCreator{""}, &fixtureCreator{" "}, &fixtureCreator{"unknown"}, &fixtureCreator{"catalog_task"}} {
		if err := r.Register(c); err == nil {
			t.Fatal("invalid creator accepted")
		}
	}
	c := &fixtureCreator{"incident"}
	if err := r.Register(c); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(c); err == nil {
		t.Fatal("duplicate accepted")
	}
	got, err := r.Get("incident")
	if err != nil || got != c {
		t.Fatal("registered creator missing")
	}
	for _, class := range []string{"", "unknown", "problem"} {
		if _, err := r.Get(class); err == nil {
			t.Fatal("missing creator succeeded")
		}
	}
}

func TestIncidentCreationRequiresInputOwnerBeforeReceipt(t *testing.T) {
	client, app, identity, command, _, _ := intakeFixture(t)
	app.registry = NewCreatorRegistry()
	if err := app.registry.Register(&fixtureCreator{"incident"}); err != nil {
		t.Fatal(err)
	}
	command.RecordClass = " incident "
	command.IntakeKind = "incident"
	identity.Channel = "http"
	_, err := app.Create(context.Background(), identity, command)
	if !errors.Is(err, creation.ErrInternalFailure) {
		t.Fatalf("expected missing owner failure, got %v", err)
	}
	if n := client.IntakeRequest.Query().CountX(context.Background()); n != 0 {
		t.Fatalf("unexpected receipts: %d", n)
	}
}

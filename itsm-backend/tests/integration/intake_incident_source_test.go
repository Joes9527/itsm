package integration

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/controller"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strings"
	"testing"
)

func TestIntakeIncidentHTTPSourceAndMetadata(t *testing.T) {
	for _, source := range []string{"", "manual", "user", "system", "monitoring"} {
		name := source
		if name == "" {
			name = "omitted"
		}
		t.Run(name, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			h := controller.NewIncidentController(nil, nil, nil, nil, nil, zap.NewNop().Sugar())
			h.SetCreationApplication(f.app)
			body := `{"title":"Incident from HTTP","priority":"high"`
			if source != "" {
				body += `,"source":"` + source + `"`
			}
			body += `}`
			w, result := intakeHTTP(t, f, h.CreateIncident, body, "source", nil)
			if source == "system" || source == "monitoring" {
				require.Equal(t, 403, w.Code, w.Body.String())
				require.Zero(t, f.client.IntakeRequest.Query().CountX(ctx))
				require.Zero(t, f.client.Ticket.Query().CountX(ctx))
				require.Zero(t, f.client.Incident.Query().CountX(ctx))
				require.Zero(t, f.client.IntakeResolutionSnapshot.Query().CountX(ctx))
				require.Zero(t, f.client.AuditLog.Query().CountX(ctx))
				require.Zero(t, f.client.OutboxEvent.Query().CountX(ctx))
				return
			}
			require.Equal(t, 201, w.Code, w.Body.String())
			want := source
			if want == "" {
				want = "manual"
			}
			require.Equal(t, want, f.client.Ticket.GetX(ctx, result.WorkItemID).Source)
			w, replay := intakeHTTP(t, f, h.CreateIncident, body, "source", nil)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.True(t, replay.Replayed)
			for _, unsafe := range []string{`"source":"system"`, `"source":"monitoring"`, `"metadata":{"nested":[{"api_key":"credential-sentinel"}]}`} {
				w, _ = intakeHTTP(t, f, h.CreateIncident, `{"title":"Incident from HTTP","priority":"high",`+unsafe+`}`, "source", nil)
				require.Contains(t, []int{400, 403}, w.Code, w.Body.String())
				require.NotContains(t, w.Body.String(), "credential-sentinel")
			}
			require.Equal(t, 1, f.client.IntakeRequest.Query().CountX(ctx))
		})
	}
}

func TestIntakeIncidentHistoricalReceiptDigestAndImmutability(t *testing.T) {
	for _, source := range []string{"", "manual", "user"} {
		name := source
		if name == "" {
			name = "omitted"
		}
		t.Run(name, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			f.identity.Channel = "http"
			command := creation.CreateWorkItemCommand{RecordClass: "incident", IntakeKind: "incident", Confirmation: "confirmed", Title: "Historical incident", Priority: "high", IdempotencyKey: "historical", Incident: &creation.IncidentInput{Source: source, Metadata: map[string]any{"title": "benign", "amount": json.Number("9007199254740993.125")}}}
			_, oldDigest, err := creation.CanonicalizeCommand(command)
			require.NoError(t, err)
			result, err := f.app.Create(ctx, f.identity, command)
			require.NoError(t, err)
			receipt := f.client.IntakeRequest.Query().OnlyX(ctx)
			require.Equal(t, "intake-v4", receipt.DigestVersion)
			require.Equal(t, oldDigest, receipt.RequestDigest)
			require.Equal(t, source, command.Incident.Source)
			require.Equal(t, json.Number("9007199254740993.125"), command.Incident.Metadata["amount"])
			// Simulate an old completed receipt whose buggy source must remain historical evidence.
			f.client.Ticket.UpdateOneID(result.WorkItemID).SetSource("http").SaveX(ctx)
			command.RecordClass = " incident "
			command.Incident.Source = " " + source + " "
			replay, err := f.app.Create(ctx, f.identity, command)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.Equal(t, result.WorkItemID, replay.WorkItemID)
			require.Equal(t, "http", f.client.Ticket.GetX(ctx, result.WorkItemID).Source)
			require.Equal(t, " "+source+" ", command.Incident.Source)
			var raw []string
			require.NoError(t, f.client.Incident.Query().Select("metadata").Scan(ctx, &raw))
			require.Contains(t, raw[0], "9007199254740993.125")
			for _, channel := range []string{"api", "monitoring", "bpmn"} {
				spoof := f.identity
				spoof.Channel = channel
				_, err = f.app.Create(ctx, spoof, command)
				require.ErrorIs(t, err, creation.ErrPermissionDenied)
			}
			command.Incident.Source = " system "
			_, err = f.app.Create(ctx, f.identity, command)
			require.ErrorIs(t, err, creation.ErrPermissionDenied)
			command.Incident.Source = source
			command.Incident.Metadata = map[string]any{"nested": []any{map[string]string{"clientSecret": "credential-sentinel"}}}
			_, err = f.app.Create(ctx, f.identity, command)
			require.ErrorIs(t, err, creation.ErrInvalidCommand)
			require.NotContains(t, err.Error(), "credential-sentinel")
			require.Equal(t, 1, f.client.IntakeRequest.Query().CountX(ctx))
			if source == "" {
				command.Incident = &creation.IncidentInput{Source: "manual", Metadata: map[string]any{"title": "benign", "amount": json.Number("9007199254740993.125")}}
				_, err = f.app.Create(ctx, f.identity, command)
				require.ErrorIs(t, err, creation.ErrIdempotencyConflict)
			}
		})
	}
}

func TestIntakeIncidentMetadataRejectsBeforeGraph(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	f.identity.Channel = "http"
	command := creation.CreateWorkItemCommand{RecordClass: " incident ", IntakeKind: "incident", Confirmation: "confirmed", Title: "Secret metadata", Priority: "high", IdempotencyKey: "metadata", Incident: &creation.IncidentInput{Metadata: map[string]any{"nested": []map[string]string{{"password": "credential-sentinel"}}}}}
	_, err := f.app.Create(ctx, f.identity, command)
	require.ErrorIs(t, err, creation.ErrInvalidCommand)
	require.False(t, strings.Contains(err.Error(), "credential-sentinel"))
	require.Zero(t, f.client.IntakeRequest.Query().CountX(ctx))
	require.Zero(t, f.client.Ticket.Query().CountX(ctx))
}

func TestIntakeIncidentForbiddenHistoricalReceiptCannotReplay(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	f.identity.Channel = "http"
	command := creation.CreateWorkItemCommand{RecordClass: "incident", IntakeKind: "incident", Confirmation: "confirmed", Title: "Old buggy incident", Priority: "high", IdempotencyKey: "old-source", Incident: &creation.IncidentInput{Source: "manual"}}
	result, err := f.app.Create(ctx, f.identity, command)
	require.NoError(t, err)
	for _, source := range []string{"system", "monitoring"} {
		command.Incident.Source = source
		command.IdempotencyKey = "historical-" + source
		_, historicalDigest, err := creation.CanonicalizeCommand(command)
		require.NoError(t, err)
		f.client.IntakeRequest.Create().SetTenantID(f.identity.TenantID).SetActorTenantID(f.identity.TenantID).SetActorID(f.identity.ActorID).SetRequesterID(f.identity.RequesterID).SetChannel("http").SetOperation("create_work_item").SetIdempotencyKey(command.IdempotencyKey).SetRequestDigest(historicalDigest).SetDigestVersion("intake-v4").SetStatus("completed").SetWorkItemID(result.WorkItemID).SaveX(ctx)
		_, err = f.app.Create(ctx, f.identity, command)
		require.ErrorIs(t, err, creation.ErrPermissionDenied)
		require.Equal(t, "manual", f.client.Ticket.GetX(ctx, result.WorkItemID).Source)
	}
}

type pointerEncodedHistoricalIncidentMetadata string

func (*pointerEncodedHistoricalIncidentMetadata) MarshalJSON() ([]byte, error) {
	return []byte(`{"password":"credential-sentinel"}`), nil
}

func TestIntakeIncidentPointerEncoderCannotReplayHistoricalReceipt(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	command := creation.CreateWorkItemCommand{RecordClass: "incident", IntakeKind: "incident", Confirmation: "confirmed", Title: "Historical metadata receipt", Priority: "high", IdempotencyKey: "original", Incident: &creation.IncidentInput{Source: "manual"}}
	original, err := f.app.Create(ctx, f.identity, command)
	require.NoError(t, err)

	// Seed historical digest evidence from the JSON object emitted by the encoder.
	command.IdempotencyKey = "historical-pointer-encoder"
	command.Incident.Metadata = map[string]any{"nested": []any{map[string]any{"password": "credential-sentinel"}}}
	_, historicalDigest, err := creation.CanonicalizeCommand(command)
	require.NoError(t, err)
	receipt := f.client.IntakeRequest.Create().SetTenantID(f.identity.TenantID).SetActorTenantID(f.identity.TenantID).SetActorID(f.identity.ActorID).SetRequesterID(f.identity.RequesterID).SetChannel("http").SetOperation("create_work_item").SetIdempotencyKey(command.IdempotencyKey).SetRequestDigest(historicalDigest).SetDigestVersion("intake-v4").SetStatus("completed").SetWorkItemID(original.WorkItemID).SaveX(ctx)

	values := []pointerEncodedHistoricalIncidentMetadata{"benign"}
	command.Incident.Metadata = map[string]any{"nested": values}
	result, err := f.app.Create(ctx, f.identity, command)
	require.ErrorIs(t, err, creation.ErrInvalidCommand)
	require.Nil(t, result)
	require.NotContains(t, err.Error(), "credential-sentinel")
	require.Equal(t, []pointerEncodedHistoricalIncidentMetadata{"benign"}, values)
	require.Equal(t, "manual", command.Incident.Source)
	require.Equal(t, historicalDigest, f.client.IntakeRequest.GetX(ctx, receipt.ID).RequestDigest)
	require.Equal(t, "intake-v4", f.client.IntakeRequest.GetX(ctx, receipt.ID).DigestVersion)
	require.Equal(t, 2, f.client.IntakeRequest.Query().CountX(ctx))
	require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
	require.Equal(t, 1, f.client.Incident.Query().CountX(ctx))
}

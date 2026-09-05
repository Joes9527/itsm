package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
)

type emailAttachmentWriter interface {
	UploadEmailAttachment(context.Context, int, *FileHeader, int, int, string, string, string) (*dto.TicketAttachmentResponse, error)
}

type recoveryStorage struct {
	objects map[string][]byte
	writes  int
}

func (s *recoveryStorage) Save(_ context.Context, key string, r io.Reader, _ int64) error {
	b, e := io.ReadAll(r)
	if e == nil {
		s.objects[key] = b
		s.writes++
	}
	return e
}
func (s *recoveryStorage) Open(_ context.Context, key string) (io.ReadCloser, int64, error) {
	b, ok := s.objects[key]
	if !ok {
		return nil, 0, errors.New("missing")
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}
func (s *recoveryStorage) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	return nil
}

func TestEmailAttachmentRecoversObjectSavedBeforeDatabaseFailure(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	tenant := client.Tenant.Create().SetName("Mail").SetCode("mail").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("mail").SetEmail("mail@example.test").SetName("Mail").SetPasswordHash("unused").SetRole("super_admin").SaveX(ctx)
	item := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetTicketNumber("MAIL-1").SetTitle("Email").SetPriority("medium").SetSource("email").SetExternalMessageID("message-one").SaveX(ctx)
	storage := &recoveryStorage{objects: map[string][]byte{}}
	svc := NewTicketAttachmentService(client, zap.NewNop().Sugar())
	svc.SetStorage(storage)
	port, ok := any(svc).(emailAttachmentWriter)
	require.True(t, ok, "email delivery needs durable source-scoped attachment persistence")
	upload := func(message, attachment string) (*dto.TicketAttachmentResponse, error) {
		return port.UploadEmailAttachment(ctx, item.ID, &FileHeader{Filename: "evidence.txt", ContentType: "text/plain", Size: 8, Reader: bytes.NewBufferString("evidence")}, actor.ID, tenant.ID, "support@example.test", message, attachment)
	}
	fail := true
	client.TicketAttachment.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if fail {
				return nil, errors.New("injected attachment database failure")
			}
			return next.Mutate(ctx, m)
		})
	})
	_, err := upload("message-one", "attachment-one")
	require.ErrorContains(t, err, "injected attachment database failure")
	require.Len(t, storage.objects, 1, "durable source references must allow reuse after storage succeeds")
	require.Zero(t, client.TicketAttachment.Query().CountX(ctx))
	fail = false
	first, err := upload("message-one", "attachment-one")
	require.NoError(t, err)
	replay, err := upload("message-one", "attachment-one")
	require.NoError(t, err)
	require.Equal(t, first.ID, replay.ID)
	require.Len(t, storage.objects, 1)
	require.Equal(t, 1, client.TicketAttachment.Query().CountX(ctx))
	_, err = upload("message-one", "attachment-two")
	require.NoError(t, err)
	require.Len(t, storage.objects, 2)
	require.Equal(t, 2, client.TicketAttachment.Query().CountX(ctx))
	_, err = upload("foreign-message", "attachment-one")
	require.Error(t, err)
	require.Len(t, storage.objects, 2)
	client.User.UpdateOneID(actor.ID).SetActive(false).ExecX(ctx)
	_, err = upload("message-one", "attachment-one")
	require.Error(t, err, "replay must recheck the active actor")
}

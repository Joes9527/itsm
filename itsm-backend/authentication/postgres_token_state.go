package authentication

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
)

var ErrTokenStateUnavailable = errors.New("authentication state unavailable")

// TokenStateStore accepts only identities created by strict token validation.
// PostgreSQL is the production owner of these append-only security facts.
type TokenStateStore interface {
	Ready(context.Context) error
	IsRevoked(context.Context, *VerifiedCredential) (bool, error)
	Revoke(context.Context, *VerifiedCredential) error
	ConsumeRefresh(context.Context, *VerifiedCredential) error
}

type postgresTokenStateStore struct {
	db          *sql.DB
	authorityID string
}

func NewPostgresTokenStateStore(db *sql.DB, authorityID string) (TokenStateStore, error) {
	id, err := uuid.Parse(authorityID)
	if db == nil || err != nil || id == uuid.Nil || id.String() != authorityID {
		return nil, errors.New("invalid authentication state configuration")
	}
	return &postgresTokenStateStore{db: db, authorityID: authorityID}, nil
}

func tokenStateUnavailable(err error) error {
	return fmt.Errorf("%w: %w", ErrTokenStateUnavailable, err)
}

func (s *postgresTokenStateStore) begin(ctx context.Context, tenant string) (*sql.Tx, error) {
	if s == nil || s.db == nil || ctx == nil {
		return nil, tokenStateUnavailable(errors.New("missing authentication state dependency"))
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, tokenStateUnavailable(err)
	}
	if _, err = tx.ExecContext(ctx, "SET LOCAL synchronous_commit=on; SET LOCAL search_path=pg_catalog,public"); err == nil {
		_, err = tx.ExecContext(ctx, "SELECT set_config('app.current_tenant',$1,true)", tenant)
	}
	if err == nil {
		// Keep the validated schema and RLS policy stable until the state read or
		// append commits; ALTER POLICY/ALTER TABLE must wait for this transaction.
		_, err = tx.ExecContext(ctx, "LOCK TABLE public.auth_state_authorities,public.auth_token_states IN ACCESS SHARE MODE")
	}
	if err == nil {
		err = s.readyTx(ctx, tx)
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, tokenStateUnavailable(err)
	}
	return tx, nil
}

func (s *postgresTokenStateStore) Ready(ctx context.Context) error {
	tx, err := s.begin(ctx, "")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = tx.Commit(); err != nil {
		return tokenStateUnavailable(err)
	}
	return nil
}

func (s *postgresTokenStateStore) IsRevoked(ctx context.Context, c *VerifiedCredential) (bool, error) {
	ctx, err := credentialContext(ctx, c, "access")
	if err != nil {
		return false, err
	}
	tx, err := s.begin(ctx, strconv.Itoa(c.tenantID))
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	found, err := s.matchesState(ctx, tx, c, "access_revocation")
	if err != nil {
		return false, tokenStateUnavailable(err)
	}
	if err = tx.Commit(); err != nil {
		return false, tokenStateUnavailable(err)
	}
	return found, nil
}

func (s *postgresTokenStateStore) matchesState(ctx context.Context, tx *sql.Tx, c *VerifiedCredential, purpose string) (bool, error) {
	var tenant, actor int
	var expiry time.Time
	err := tx.QueryRowContext(ctx, `SELECT tenant_id,actor_id,expires_at FROM public.auth_token_states WHERE authority_id=$1 AND purpose=$2 AND token_digest=$3`, s.authorityID, purpose, c.digest).Scan(&tenant, &actor, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if tenant != c.tenantID || actor != c.actorID || !expiry.Equal(c.expiresAt) {
		return false, errors.New("token state identity mismatch")
	}
	return true, nil
}

func (s *postgresTokenStateStore) Revoke(ctx context.Context, c *VerifiedCredential) error {
	return s.record(ctx, c, "access", "access_revocation", false)
}

func (s *postgresTokenStateStore) ConsumeRefresh(ctx context.Context, c *VerifiedCredential) error {
	return s.record(ctx, c, "refresh", "refresh_consumption", true)
}

func (s *postgresTokenStateStore) record(ctx context.Context, c *VerifiedCredential, tokenPurpose, statePurpose string, once bool) error {
	ctx, err := credentialContext(ctx, c, tokenPurpose)
	if err != nil {
		return err
	}
	tx, err := s.begin(ctx, strconv.Itoa(c.tenantID))
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = credentialContext(ctx, c, tokenPurpose); err != nil {
		return err
	}
	var inserted string
	err = tx.QueryRowContext(ctx, `INSERT INTO public.auth_token_states(authority_id,purpose,token_digest,tenant_id,actor_id,expires_at,recorded_at)
 VALUES($1,$2,$3,$4,$5,$6,CURRENT_TIMESTAMP)
 ON CONFLICT(authority_id,purpose,token_digest) DO NOTHING RETURNING token_digest`, s.authorityID, statePurpose, c.digest, c.tenantID, c.actorID, c.expiresAt).Scan(&inserted)
	if errors.Is(err, sql.ErrNoRows) {
		if once {
			return ErrRefreshTokenConsumed
		}
		found, verifyErr := s.matchesState(ctx, tx, c, statePurpose)
		if verifyErr != nil {
			return tokenStateUnavailable(verifyErr)
		}
		if !found {
			return tokenStateUnavailable(errors.New("conflicting token state is not visible"))
		}
	} else if err != nil {
		return tokenStateUnavailable(err)
	}
	if err = tx.Commit(); err != nil {
		return tokenStateUnavailable(err)
	}
	return nil
}

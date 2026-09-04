package service

import (
	"context"
	"database/sql"
	"strconv"
)

type VectorStore struct{ db *sql.DB }

func NewVectorStore(db *sql.DB) *VectorStore { return &VectorStore{db: db} }

// TestConnection checks if the vectors table exists and is accessible
func (s *VectorStore) TestConnection() error {
	// Simple query to verify the vectors table exists
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM vectors LIMIT 1").Scan(&count)
	return err
}

// CheckAvailability is the runtime-only pgvector capability probe. Schema
// installation belongs exclusively to the migration bootstrap, so this path
// intentionally performs one read-only catalog query and never repairs state.
func (s *VectorStore) CheckAvailability(ctx context.Context) error {
	if s == nil || s.db == nil {
		return sql.ErrConnDone
	}
	var available bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_extension extension_record
			JOIN pg_namespace namespace ON namespace.oid = extension_record.extnamespace
			WHERE extension_record.extname = 'vector'
			  AND to_regtype(format('%I.vector', namespace.nspname)) IS NOT NULL
		) AND to_regclass(format('%I.vectors', current_schema())) IS NOT NULL
	`).Scan(&available)
	if err != nil || !available {
		return &vectorCapabilityError{cause: err}
	}
	return nil
}

type vectorCapabilityError struct{ cause error }

func (*vectorCapabilityError) Error() string     { return "pgvector runtime capability is unavailable" }
func (err *vectorCapabilityError) Unwrap() error { return err.cause }

func (s *VectorStore) Upsert(ctx context.Context, tenantID int, objectType string, objectID int, embedding []float32, content string, source string) error {
	// pgx prefers []float32 -> vector; with database/sql we build string literal
	// Convert to SQL literal: '['1,2,3']' style for pgvector (space-separated)
	values := make([]byte, 0, len(embedding)*6)
	values = append(values, '[')
	for i, v := range embedding {
		if i > 0 {
			values = append(values, ',')
		}
		values = append(values, []byte(fmtFloat(v))...)
	}
	values = append(values, ']')
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO vectors(tenant_id, object_type, object_id, embedding, content, source)
        VALUES ($1,$2,$3,$4::vector,$5,$6)
        ON CONFLICT (tenant_id, object_type, object_id) DO UPDATE
        SET embedding = EXCLUDED.embedding, content = EXCLUDED.content, source = EXCLUDED.source;
    `, tenantID, objectType, objectID, string(values), content, source)
	return err
}

func (s *VectorStore) SearchTopK(ctx context.Context, tenantID int, query []float32, k int) (*sql.Rows, error) {
	if k <= 0 {
		k = 5
	}
	values := make([]byte, 0, len(query)*6)
	values = append(values, '[')
	for i, v := range query {
		if i > 0 {
			values = append(values, ',')
		}
		values = append(values, []byte(fmtFloat(v))...)
	}
	values = append(values, ']')
	return s.db.QueryContext(ctx, `
        SELECT object_type, object_id, content, source, (embedding <#> $1::vector) AS distance
        FROM vectors WHERE tenant_id = $2
        ORDER BY embedding <#> $1::vector
        LIMIT $3;
    `, string(values), tenantID, k)
}

// SearchTopKByType allows restricting vector search to a specific object_type (e.g., 'kb' or 'incident')
func (s *VectorStore) SearchTopKByType(ctx context.Context, tenantID int, objectType string, query []float32, k int) (*sql.Rows, error) {
	if k <= 0 {
		k = 5
	}
	values := make([]byte, 0, len(query)*6)
	values = append(values, '[')
	for i, v := range query {
		if i > 0 {
			values = append(values, ',')
		}
		values = append(values, []byte(fmtFloat(v))...)
	}
	values = append(values, ']')
	return s.db.QueryContext(ctx, `
        SELECT object_type, object_id, content, source, (embedding <#> $1::vector) AS distance
        FROM vectors WHERE tenant_id = $2 AND object_type = $3
        ORDER BY embedding <#> $1::vector
        LIMIT $4;
    `, string(values), tenantID, objectType, k)
}

func fmtFloat(f float32) string {
	// compact but precise enough for embeddings
	return strconv.FormatFloat(float64(f), 'f', 6, 64)
}

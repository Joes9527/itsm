-- RCA metadata bootstrap and retirement of the duplicate root-cause body.
-- Run with the application's explicit search_path and ON_ERROR_STOP after backup,
-- with Problem/RCA writers stopped. The single DO statement is atomic, including DDL.
-- Operators grant the configured runtime role table/sequence access separately; this
-- migration never assumes a role name. Enable tenant-scoped SQL connections before
-- routing RCA traffic to the new service. Do not restart an old binary after retirement.
-- Conflicting bodies, duplicate analyses, and orphan/cross-tenant metadata block the
-- migration without changing data. Reconcile those records explicitly, then retry.
-- After deployment, rollback requires restoring the backup and the previous binary
-- together: the retired column is not recreated or dual-written by the new service.
DO $rca$
DECLARE
    bad_ids text;
    previous_row_security text := current_setting('row_security');
BEGIN
    -- Do not migrate a partial RLS view or accidentally resolve another schema.
    PERFORM set_config('row_security', 'off', true);
    IF (SELECT count(*) FROM information_schema.tables
        WHERE table_schema = current_schema() AND table_name IN ('problems', 'tickets', 'users')) <> 3 THEN
        RAISE EXCEPTION 'Problem RCA migration requires problems, tickets, users in current_schema';
    END IF;
    LOCK TABLE problems, tickets, users IN SHARE ROW EXCLUSIVE MODE;
    CREATE TABLE IF NOT EXISTS problem_root_cause_analyses (
        id BIGSERIAL PRIMARY KEY,
        problem_id BIGINT NOT NULL REFERENCES problems(id),
        analyst_id BIGINT NOT NULL REFERENCES users(id),
        analysis_method TEXT NOT NULL,
        contributing_factors TEXT,
        evidence TEXT,
        confidence_level TEXT NOT NULL,
        analysis_date TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
        reviewed_by BIGINT REFERENCES users(id),
        review_date TIMESTAMPTZ,
        created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
        updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
    );
    LOCK TABLE problem_root_cause_analyses IN ACCESS EXCLUSIVE MODE;

    SELECT string_agg(r.id::text, ',' ORDER BY r.id) INTO bad_ids
    FROM problem_root_cause_analyses r
    LEFT JOIN problems p ON p.id = r.problem_id
    LEFT JOIN tickets wi ON wi.id = p.work_item_id
    LEFT JOIN users analyst ON analyst.id = r.analyst_id
    LEFT JOIN users reviewer ON reviewer.id = r.reviewed_by
    WHERE wi.id IS NULL OR analyst.id IS NULL
       OR analyst.tenant_id IS DISTINCT FROM wi.tenant_id
       OR (r.reviewed_by IS NOT NULL AND
           (reviewer.id IS NULL OR reviewer.tenant_id IS DISTINCT FROM wi.tenant_id));
    IF bad_ids IS NOT NULL THEN
        RAISE EXCEPTION 'Invalid RCA ownership at analysis IDs %; reconcile before retry', bad_ids;
    END IF;

    SELECT string_agg(problem_id::text, ',' ORDER BY problem_id) INTO bad_ids
    FROM (SELECT problem_id FROM problem_root_cause_analyses GROUP BY problem_id HAVING count(*) > 1) duplicates;
    IF bad_ids IS NOT NULL THEN
        RAISE EXCEPTION 'Multiple RCA metadata records at Problem IDs %; reconcile before retry', bad_ids;
    END IF;

    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = current_schema() AND table_name = 'problem_root_cause_analyses'
                 AND column_name = 'root_cause_description') THEN
        EXECUTE $check$
            SELECT string_agg(p.id::text, ',' ORDER BY p.id)
            FROM problems p JOIN problem_root_cause_analyses r ON r.problem_id = p.id
            WHERE btrim(COALESCE(p.root_cause, '')) <> ''
              AND btrim(COALESCE(r.root_cause_description, '')) <> ''
              AND p.root_cause IS DISTINCT FROM r.root_cause_description
        $check$ INTO bad_ids;
        IF bad_ids IS NOT NULL THEN
            RAISE EXCEPTION 'Conflicting root-cause bodies at Problem IDs %; reconcile before retry', bad_ids;
        END IF;
        EXECUTE $backfill$
            UPDATE problems p SET root_cause = r.root_cause_description
            FROM problem_root_cause_analyses r
            WHERE r.problem_id = p.id AND btrim(COALESCE(p.root_cause, '')) = ''
              AND btrim(COALESCE(r.root_cause_description, '')) <> ''
        $backfill$;
        ALTER TABLE problem_root_cause_analyses DROP COLUMN root_cause_description;
    END IF;
    CREATE UNIQUE INDEX IF NOT EXISTS problem_rca_problem_id_unique ON problem_root_cause_analyses (problem_id);
    IF EXISTS (SELECT 1 FROM pg_policies WHERE schemaname = current_schema()
               AND tablename = 'problem_root_cause_analyses' AND policyname <> 'problem_rca_tenant_isolation') THEN
        RAISE EXCEPTION 'Unknown RCA RLS policies; reconcile policy definitions before retry';
    END IF;
    ALTER TABLE problem_root_cause_analyses ENABLE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS problem_rca_tenant_isolation ON problem_root_cause_analyses;
    CREATE POLICY problem_rca_tenant_isolation ON problem_root_cause_analyses
    USING (EXISTS (
        SELECT 1 FROM problems p JOIN tickets wi ON wi.id = p.work_item_id
        WHERE p.id = problem_root_cause_analyses.problem_id
          AND wi.tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint
          AND wi.deleted_at IS NULL
    ));
    PERFORM set_config('row_security', previous_row_security, true);
END $rca$;

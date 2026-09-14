-- Empty-table schema only: no historical verification or lifecycle backfill.
ALTER TABLE problems ADD COLUMN IF NOT EXISTS verified_version bigint;
ALTER TABLE problems ADD COLUMN IF NOT EXISTS verification_digest varchar(255);
ALTER TABLE problems ADD COLUMN IF NOT EXISTS verified_by bigint;
ALTER TABLE problems ADD COLUMN IF NOT EXISTS verified_at timestamptz;
ALTER TABLE problems ADD COLUMN IF NOT EXISTS verification_note text;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='problems'::regclass AND conname='problem_resolution_verifier_fk') THEN
  ALTER TABLE problems ADD CONSTRAINT problem_resolution_verifier_fk FOREIGN KEY(verified_by) REFERENCES users(id);
 END IF;
END $$;

CREATE TABLE IF NOT EXISTS problem_investigations (
 id bigserial PRIMARY KEY,
 problem_id bigint NOT NULL UNIQUE REFERENCES problems(id),
 investigator_id bigint NOT NULL REFERENCES users(id),
 status text NOT NULL DEFAULT 'in_progress' CHECK (status IN ('not_started','in_progress','on_hold','completed','cancelled')),
 start_date timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 estimated_completion_date timestamptz, actual_completion_date timestamptz,
 investigation_summary text,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS problem_investigation_steps (
 id bigserial PRIMARY KEY,
 investigation_id bigint NOT NULL REFERENCES problem_investigations(id),
 step_number integer NOT NULL CHECK (step_number > 0),
 step_title text NOT NULL, step_description text NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','in_progress','completed','blocked','cancelled')),
 assigned_to bigint REFERENCES users(id), start_date timestamptz, completion_date timestamptz, notes text,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(investigation_id,step_number)
);
CREATE TABLE IF NOT EXISTS problem_solutions (
 id bigserial PRIMARY KEY,
 problem_id bigint NOT NULL REFERENCES problems(id),
 solution_type text NOT NULL CHECK (solution_type IN ('workaround','fix','prevention','process')),
 solution_description text NOT NULL, proposed_by bigint NOT NULL REFERENCES users(id),
 proposed_date timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 status text NOT NULL DEFAULT 'proposed', priority text NOT NULL,
 estimated_effort_hours integer, estimated_cost double precision, risk_assessment text,
 approval_status text NOT NULL DEFAULT 'pending', approved_by bigint REFERENCES users(id), approval_date timestamptz,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);

ALTER TABLE problem_investigations ENABLE ROW LEVEL SECURITY;
ALTER TABLE problem_investigations FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation_problem_investigations ON problem_investigations;
CREATE POLICY tenant_isolation_problem_investigations ON problem_investigations
 USING(EXISTS(SELECT 1 FROM problems p JOIN tickets w ON w.id=p.work_item_id WHERE p.id=problem_id AND w.deleted_at IS NULL AND w.tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint))
 WITH CHECK(EXISTS(SELECT 1 FROM problems p JOIN tickets w ON w.id=p.work_item_id JOIN users u ON u.id=investigator_id AND u.tenant_id=w.tenant_id WHERE p.id=problem_id AND w.deleted_at IS NULL AND w.tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint));
ALTER TABLE problem_investigation_steps ENABLE ROW LEVEL SECURITY;
ALTER TABLE problem_investigation_steps FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation_problem_investigation_steps ON problem_investigation_steps;
CREATE POLICY tenant_isolation_problem_investigation_steps ON problem_investigation_steps
 USING(EXISTS(SELECT 1 FROM problem_investigations i WHERE i.id=investigation_id))
 WITH CHECK(EXISTS(SELECT 1 FROM problem_investigations i JOIN problems p ON p.id=i.problem_id JOIN tickets w ON w.id=p.work_item_id WHERE i.id=investigation_id AND (assigned_to IS NULL OR EXISTS(SELECT 1 FROM users u WHERE u.id=assigned_to AND u.tenant_id=w.tenant_id))));
ALTER TABLE problem_solutions ENABLE ROW LEVEL SECURITY;
ALTER TABLE problem_solutions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation_problem_solutions ON problem_solutions;
CREATE POLICY tenant_isolation_problem_solutions ON problem_solutions
 USING(EXISTS(SELECT 1 FROM problems p JOIN tickets w ON w.id=p.work_item_id WHERE p.id=problem_id AND w.deleted_at IS NULL AND w.tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint))
 WITH CHECK(EXISTS(SELECT 1 FROM problems p JOIN tickets w ON w.id=p.work_item_id JOIN users u ON u.id=proposed_by AND u.tenant_id=w.tenant_id WHERE p.id=problem_id AND w.deleted_at IS NULL AND w.tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint AND (approved_by IS NULL OR EXISTS(SELECT 1 FROM users reviewer WHERE reviewer.id=approved_by AND reviewer.tenant_id=w.tenant_id))));

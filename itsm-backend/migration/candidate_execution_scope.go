package migration

const CandidateExecutionScopeVersion = "039_candidate_execution_scope"

const candidateExecutionScopeSQL = `
DO $$ BEGIN
    IF current_schema() <> 'public' THEN
        RAISE EXCEPTION 'candidate execution scope requires explicitly selected public schema';
    END IF;
END $$;
CREATE TABLE public.execution_scopes (
    id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    deployment_id text NOT NULL CHECK (deployment_id ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
    tenant_id bigint NOT NULL REFERENCES public.tenants(id),
    status text NOT NULL CHECK (status IN ('active','closed')),
    created_by bigint NOT NULL REFERENCES public.users(id),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX execution_scope_active_tenant ON public.execution_scopes(deployment_id,tenant_id) WHERE status='active';
CREATE TABLE public.execution_runtime_bindings (
    runtime_role name PRIMARY KEY,
    deployment_id text NOT NULL CHECK (deployment_id ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
    mode text NOT NULL CHECK (mode IN ('standard','candidate'))
);
CREATE TABLE public.execution_scope_members (
    scope_id uuid NOT NULL REFERENCES public.execution_scopes(id),
    work_item_id bigint PRIMARY KEY REFERENCES public.tickets(id),
    registered_by name NOT NULL DEFAULT session_user,
    registered_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(scope_id,work_item_id)
);
ALTER TABLE public.execution_scopes ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.execution_runtime_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.execution_scope_members ENABLE ROW LEVEL SECURITY;
CREATE POLICY execution_binding_read ON public.execution_runtime_bindings FOR SELECT
USING (runtime_role=session_user);
CREATE POLICY execution_scope_read ON public.execution_scopes FOR SELECT
USING (tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint
AND EXISTS(SELECT 1 FROM public.execution_runtime_bindings b
WHERE b.runtime_role=session_user AND b.deployment_id=execution_scopes.deployment_id AND b.mode='candidate'));
CREATE POLICY execution_member_read ON public.execution_scope_members FOR SELECT
USING (EXISTS(SELECT 1 FROM public.execution_scopes s WHERE s.id=execution_scope_members.scope_id));
REVOKE ALL ON public.execution_scopes,public.execution_runtime_bindings,public.execution_scope_members FROM PUBLIC;

-- The owner-only trigger is the sole enrollment path. Runtime roles receive SELECT
-- through explicit deployment preparation, never direct membership writes.
CREATE FUNCTION public.register_new_execution_member() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE
    binding public.execution_runtime_bindings%ROWTYPE;
    chosen public.execution_scopes%ROWTYPE;
    scope_setting text;
BEGIN
	IF TG_RELID <> 'public.tickets'::regclass OR TG_OP <> 'INSERT' OR TG_WHEN <> 'AFTER' OR TG_LEVEL <> 'ROW' THEN
	    RAISE EXCEPTION 'execution enrollment requires the tickets insert trigger' USING ERRCODE='42501';
	END IF;
    SELECT * INTO binding FROM public.execution_runtime_bindings WHERE runtime_role=session_user FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION 'execution runtime binding required' USING ERRCODE='42501'; END IF;
    IF binding.mode='standard' THEN RETURN NEW; END IF;
    scope_setting := NULLIF(current_setting('app.execution_scope_id',true),'');
    IF scope_setting IS NULL THEN RAISE EXCEPTION 'execution scope required' USING ERRCODE='42501'; END IF;
    SELECT * INTO chosen FROM public.execution_scopes WHERE id=scope_setting::uuid FOR SHARE;
    IF NOT FOUND OR chosen.status<>'active' OR chosen.deployment_id<>binding.deployment_id
       OR chosen.tenant_id<>NEW.tenant_id
       OR NULLIF(current_setting('app.current_tenant',true),'')::bigint IS DISTINCT FROM NEW.tenant_id THEN
        RAISE EXCEPTION 'execution scope mismatch' USING ERRCODE='42501';
    END IF;
    INSERT INTO public.execution_scope_members(scope_id,work_item_id,registered_by) VALUES(chosen.id,NEW.id,session_user);
    RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION public.register_new_execution_member() FROM PUBLIC;
CREATE TRIGGER register_new_execution_member AFTER INSERT ON public.tickets
FOR EACH ROW EXECUTE FUNCTION public.register_new_execution_member();

-- Execution provenance is structural and immutable. Existing rows remain NULL.
ALTER TABLE public.outbox_events ADD COLUMN IF NOT EXISTS execution_work_item_id bigint;
ALTER TABLE public.process_instances ADD COLUMN IF NOT EXISTS execution_work_item_id bigint;
ALTER TABLE public.outbox_events ADD CONSTRAINT outbox_execution_work_item_fk FOREIGN KEY(execution_work_item_id) REFERENCES public.tickets(id);
ALTER TABLE public.process_instances ADD CONSTRAINT process_execution_work_item_fk FOREIGN KEY(execution_work_item_id) REFERENCES public.tickets(id);
CREATE FUNCTION public.preserve_execution_work_item_reference() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF TG_RELID NOT IN ('public.outbox_events'::regclass,'public.process_instances'::regclass)
       OR TG_OP <> 'UPDATE' OR TG_WHEN <> 'BEFORE' OR TG_LEVEL <> 'ROW' THEN
        RAISE EXCEPTION 'invalid execution reference trigger context';
    END IF;
    IF NEW.execution_work_item_id IS DISTINCT FROM OLD.execution_work_item_id THEN
        RAISE EXCEPTION 'execution WorkItem reference is immutable';
    END IF;
    RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION public.preserve_execution_work_item_reference() FROM PUBLIC;
CREATE TRIGGER outbox_execution_reference_immutable BEFORE UPDATE OF execution_work_item_id ON public.outbox_events
FOR EACH ROW EXECUTE FUNCTION public.preserve_execution_work_item_reference();
CREATE TRIGGER process_execution_reference_immutable BEFORE UPDATE OF execution_work_item_id ON public.process_instances
FOR EACH ROW EXECUTE FUNCTION public.preserve_execution_work_item_reference();

-- Default ACLs can grant roles privileges beyond PUBLIC. Strip those grants from
-- these new objects; deployment preparation later grants only the reviewed reads.
DO $$
DECLARE permission record;
BEGIN
    FOR permission IN
        SELECT DISTINCT c.oid::regclass AS object_name, a.grantee
        FROM pg_catalog.pg_class c
        CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(c.relacl,pg_catalog.acldefault('r',c.relowner))) a
        WHERE c.oid IN ('public.execution_scopes'::regclass,'public.execution_scope_members'::regclass,'public.execution_runtime_bindings'::regclass)
          AND a.grantee <> 0 AND a.grantee <> c.relowner
    LOOP
        EXECUTE format('REVOKE ALL ON TABLE %s FROM %I',permission.object_name,pg_catalog.pg_get_userbyid(permission.grantee));
    END LOOP;
    FOR permission IN
        SELECT DISTINCT p.oid::regprocedure AS object_name, a.grantee
        FROM pg_catalog.pg_proc p
        CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(p.proacl,pg_catalog.acldefault('f',p.proowner))) a
        WHERE p.oid IN ('public.register_new_execution_member()'::regprocedure,'public.preserve_execution_work_item_reference()'::regprocedure) AND a.grantee<>0 AND a.grantee<>p.proowner
    LOOP
        EXECUTE format('REVOKE ALL ON FUNCTION %s FROM %I',permission.object_name,pg_catalog.pg_get_userbyid(permission.grantee));
    END LOOP;
END $$;

`

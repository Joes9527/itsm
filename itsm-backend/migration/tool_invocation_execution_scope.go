package migration

const ToolInvocationExecutionScopeVersion = "041_tool_invocation_execution_scope"

const toolInvocationExecutionScopeSQL = `
DO $$ BEGIN
 IF current_schema() <> 'public' THEN
  RAISE EXCEPTION 'tool execution scope requires explicitly selected public schema';
 END IF;
END $$;
CREATE UNIQUE INDEX execution_scope_id_tenant ON public.execution_scopes(id,tenant_id);
CREATE UNIQUE INDEX tool_invocation_id_tenant ON public.tool_invocations(id,tenant_id);
CREATE TABLE public.execution_tool_invocations (
 invocation_id bigint PRIMARY KEY,
 tenant_id bigint NOT NULL,
 scope_id uuid NOT NULL,
 registered_by name NOT NULL DEFAULT session_user,
 registered_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(invocation_id,tenant_id) REFERENCES public.tool_invocations(id,tenant_id),
 FOREIGN KEY(scope_id,tenant_id) REFERENCES public.execution_scopes(id,tenant_id)
);
ALTER TABLE public.execution_tool_invocations ENABLE ROW LEVEL SECURITY;
CREATE POLICY execution_tool_read ON public.execution_tool_invocations FOR SELECT
USING (tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint
AND EXISTS(SELECT 1 FROM public.execution_scopes s WHERE s.id=execution_tool_invocations.scope_id AND s.tenant_id=execution_tool_invocations.tenant_id));
REVOKE ALL ON public.execution_tool_invocations FROM PUBLIC;

-- Enrollment is inseparable from the initial INSERT; approval cannot enroll history.
CREATE FUNCTION public.register_new_execution_tool_invocation() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE binding public.execution_runtime_bindings%ROWTYPE;
 chosen public.execution_scopes%ROWTYPE;
 scope_setting text;
BEGIN
 IF TG_RELID <> 'public.tool_invocations'::regclass OR TG_OP <> 'INSERT' OR TG_WHEN <> 'AFTER' OR TG_LEVEL <> 'ROW' THEN
  RAISE EXCEPTION 'tool enrollment requires the invocation insert trigger' USING ERRCODE='42501';
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
 INSERT INTO public.execution_tool_invocations(invocation_id,tenant_id,scope_id,registered_by)
 VALUES(NEW.id,NEW.tenant_id,chosen.id,session_user);
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION public.register_new_execution_tool_invocation() FROM PUBLIC;
CREATE TRIGGER register_new_execution_tool_invocation AFTER INSERT ON public.tool_invocations
FOR EACH ROW EXECUTE FUNCTION public.register_new_execution_tool_invocation();

-- Strip role-specific default ACLs as well as PUBLIC privileges.
DO $$ DECLARE permission record; BEGIN
 FOR permission IN
  SELECT DISTINCT a.grantee FROM pg_catalog.pg_class c
  CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(c.relacl,pg_catalog.acldefault('r',c.relowner))) a
  WHERE c.oid='public.execution_tool_invocations'::regclass AND a.grantee<>0 AND a.grantee<>c.relowner
 LOOP
  EXECUTE format('REVOKE ALL ON TABLE public.execution_tool_invocations FROM %I',pg_catalog.pg_get_userbyid(permission.grantee));
 END LOOP;
 FOR permission IN
  SELECT DISTINCT a.grantee FROM pg_catalog.pg_proc p
  CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(p.proacl,pg_catalog.acldefault('f',p.proowner))) a
  WHERE p.oid='public.register_new_execution_tool_invocation()'::regprocedure AND a.grantee<>0 AND a.grantee<>p.proowner
 LOOP
  EXECUTE format('REVOKE ALL ON FUNCTION public.register_new_execution_tool_invocation() FROM %I',pg_catalog.pg_get_userbyid(permission.grantee));
 END LOOP;
END $$;
`

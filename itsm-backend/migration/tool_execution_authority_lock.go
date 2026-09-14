package migration

const ToolExecutionAuthorityLockVersion = "042_tool_execution_authority_lock"

const toolExecutionAuthorityLockSQL = `
DO $$ BEGIN
 IF current_schema() <> 'public' THEN RAISE EXCEPTION 'tool authority requires public schema'; END IF;
END $$;
-- Read-only callers acquire authority locks without receiving configuration DML.
CREATE FUNCTION public.lock_candidate_tool_authority(scope_id uuid, deployment text, tenant bigint, invocation bigint)
RETURNS SETOF bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE binding public.execution_runtime_bindings%ROWTYPE;
 chosen public.execution_scopes%ROWTYPE;
 source public.execution_tool_invocations%ROWTYPE;
BEGIN
 IF NOT has_table_privilege(session_user,'public.execution_tool_invocations','SELECT') THEN
  RAISE EXCEPTION 'tool origin read privilege required' USING ERRCODE='42501';
 END IF;
 -- Same order as first-INSERT enrollment: binding, scope, then source.
 SELECT * INTO binding FROM public.execution_runtime_bindings WHERE runtime_role=session_user FOR SHARE;
 IF NOT FOUND OR binding.mode<>'candidate' OR binding.deployment_id IS DISTINCT FROM deployment THEN RETURN; END IF;
 IF scope_id IS NULL OR tenant IS NULL OR invocation IS NULL OR tenant<=0 OR invocation<=0
  OR NULLIF(current_setting('app.execution_scope_id',true),'') IS DISTINCT FROM scope_id::text
  OR NULLIF(current_setting('app.current_tenant',true),'') IS DISTINCT FROM tenant::text THEN RETURN; END IF;
 SELECT * INTO chosen FROM public.execution_scopes s WHERE s.id=scope_id FOR SHARE;
 IF NOT FOUND OR chosen.status<>'active' OR chosen.deployment_id IS DISTINCT FROM deployment
  OR chosen.tenant_id IS DISTINCT FROM tenant THEN RETURN; END IF;
 SELECT * INTO source FROM public.execution_tool_invocations m
 WHERE m.invocation_id=invocation AND m.tenant_id=tenant AND m.scope_id=chosen.id FOR SHARE;
 IF NOT FOUND THEN RETURN; END IF;
 IF NOT EXISTS(SELECT 1 FROM public.tool_invocations i WHERE i.id=invocation AND i.tenant_id=tenant) THEN RETURN; END IF;
 RETURN NEXT invocation;
END $$;
REVOKE ALL ON FUNCTION public.lock_candidate_tool_authority(uuid,text,bigint,bigint) FROM PUBLIC;
DO $$ DECLARE permission record; BEGIN
 FOR permission IN
  SELECT DISTINCT a.grantee FROM pg_catalog.pg_proc p
  CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(p.proacl,pg_catalog.acldefault('f',p.proowner))) a
  WHERE p.oid='public.lock_candidate_tool_authority(uuid,text,bigint,bigint)'::regprocedure
   AND a.grantee<>0 AND a.grantee<>p.proowner
 LOOP
  EXECUTE format('REVOKE ALL ON FUNCTION public.lock_candidate_tool_authority(uuid,text,bigint,bigint) FROM %I',pg_catalog.pg_get_userbyid(permission.grantee));
 END LOOP;
END $$;
`

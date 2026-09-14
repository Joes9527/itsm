package migration

const AuthTokenStateVersion = "046_auth_token_state"

const authTokenStateSQL = `
DO $$ BEGIN
 IF current_schema() <> 'public' THEN
  RAISE EXCEPTION 'authentication state requires explicitly selected public schema';
 END IF;
END $$;
CREATE TABLE public.auth_state_authorities (
 authority_id uuid PRIMARY KEY,
 deployment_id text NOT NULL CHECK (deployment_id <> '' AND deployment_id = btrim(deployment_id)),
 schema_version integer NOT NULL CHECK (schema_version = 1),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE public.auth_token_states (
 authority_id uuid NOT NULL REFERENCES public.auth_state_authorities(authority_id),
 purpose text NOT NULL CHECK (purpose IN ('access_revocation','refresh_consumption')),
 token_digest text NOT NULL CHECK (token_digest ~ '^[0-9a-f]{64}$'),
 tenant_id bigint NOT NULL CHECK (tenant_id > 0),
 actor_id bigint NOT NULL CHECK (actor_id > 0),
 expires_at timestamptz NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY (authority_id,purpose,token_digest)
);
ALTER TABLE public.auth_token_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.auth_token_states FORCE ROW LEVEL SECURITY;
CREATE POLICY auth_token_state_tenant ON public.auth_token_states
 USING (tenant_id = NULLIF(current_setting('app.current_tenant',true),'')::bigint)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant',true),'')::bigint);
REVOKE ALL ON public.auth_state_authorities,public.auth_token_states FROM PUBLIC;
-- Discard inherited default grants. Deployment explicitly grants only SELECT
-- on authority and SELECT/INSERT on token states to the admitted runtime role.
DO $$ DECLARE permission record; BEGIN
 FOR permission IN
  SELECT DISTINCT c.relname,a.grantee FROM pg_catalog.pg_class c
  JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
  CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(c.relacl,pg_catalog.acldefault('r',c.relowner))) a
  WHERE n.nspname='public' AND c.relname IN ('auth_state_authorities','auth_token_states')
  AND a.grantee<>0 AND a.grantee<>c.relowner
 LOOP
  EXECUTE format('REVOKE ALL ON public.%I FROM %I',permission.relname,pg_catalog.pg_get_userbyid(permission.grantee));
 END LOOP;
END $$;
`

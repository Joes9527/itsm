package migration

const NotificationConnectorTargetVersion = "044_notification_connector_target"

const notificationConnectorTargetSQL = `
DO $$ BEGIN
 IF current_schema() <> 'public' THEN
  RAISE EXCEPTION 'notification target requires explicitly selected public schema';
 END IF;
END $$;
ALTER TABLE public.ticket_notifications
 ADD COLUMN IF NOT EXISTS target_protocol_version bigint,
 ADD COLUMN IF NOT EXISTS target_connector_name text,
 ADD COLUMN IF NOT EXISTS target_connector_provider text,
 ADD COLUMN IF NOT EXISTS target_destination_digest text;
ALTER TABLE public.ticket_notifications ADD CONSTRAINT ticket_notification_connector_target_complete CHECK (
 (target_protocol_version IS NULL AND target_connector_name IS NULL AND target_connector_provider IS NULL AND target_destination_digest IS NULL)
 OR (target_protocol_version IS NOT NULL AND target_protocol_version=1
 AND target_connector_name IS NOT NULL AND target_connector_name=channel AND target_connector_name<>'' AND target_connector_name=btrim(target_connector_name)
 AND channel NOT IN ('in_app','email','push')
 AND target_connector_provider IS NOT NULL AND target_connector_provider<>'' AND target_connector_provider=btrim(target_connector_provider)
 AND target_destination_digest IS NOT NULL AND target_destination_digest ~ '^[0-9a-f]{64}$')
);
CREATE FUNCTION public.preserve_notification_connector_target() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_RELID <> 'public.ticket_notifications'::regclass OR TG_OP <> 'UPDATE' OR TG_WHEN <> 'BEFORE' OR TG_LEVEL <> 'ROW' THEN
  RAISE EXCEPTION 'invalid notification target trigger context';
 END IF;
 IF NEW.target_protocol_version IS DISTINCT FROM OLD.target_protocol_version
 OR NEW.target_connector_name IS DISTINCT FROM OLD.target_connector_name
 OR NEW.target_connector_provider IS DISTINCT FROM OLD.target_connector_provider
 OR NEW.target_destination_digest IS DISTINCT FROM OLD.target_destination_digest
 OR (OLD.target_protocol_version IS NOT NULL AND (
 NEW.id IS DISTINCT FROM OLD.id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
 OR NEW.ticket_id IS DISTINCT FROM OLD.ticket_id OR NEW.user_id IS DISTINCT FROM OLD.user_id
 OR NEW.channel IS DISTINCT FROM OLD.channel OR NEW.type IS DISTINCT FROM OLD.type
 OR NEW.content IS DISTINCT FROM OLD.content OR NEW.delivery_key IS DISTINCT FROM OLD.delivery_key)) THEN
  RAISE EXCEPTION 'notification target and delivery identity are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER notification_connector_target_immutable BEFORE UPDATE ON public.ticket_notifications
FOR EACH ROW EXECUTE FUNCTION public.preserve_notification_connector_target();
REVOKE ALL ON FUNCTION public.preserve_notification_connector_target() FROM PUBLIC;
DO $$ DECLARE permission record; BEGIN
 FOR permission IN
  SELECT a.grantee FROM pg_catalog.pg_proc p
  CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(p.proacl,pg_catalog.acldefault('f',p.proowner))) a
  WHERE p.oid='public.preserve_notification_connector_target()'::regprocedure AND a.grantee<>0 AND a.grantee<>p.proowner
 LOOP
  EXECUTE format('REVOKE ALL ON FUNCTION public.preserve_notification_connector_target() FROM %I',pg_catalog.pg_get_userbyid(permission.grantee));
 END LOOP;
END $$;
`

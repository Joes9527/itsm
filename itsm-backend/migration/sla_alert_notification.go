package migration

const SLAAlertNotificationVersion = "040_sla_alert_notification_provenance"

const slaAlertNotificationSQL = `
DO $$ BEGIN
 IF current_schema() <> 'public' THEN
  RAISE EXCEPTION 'SLA notification provenance requires explicitly selected public schema';
 END IF;
END $$;
ALTER TABLE public.sla_alert_histories ADD COLUMN IF NOT EXISTS notification_tracking_version bigint;
ALTER TABLE public.ticket_notifications ADD COLUMN IF NOT EXISTS sla_alert_history_id bigint;
ALTER TABLE public.sla_alert_histories ADD CONSTRAINT sla_alert_tracking_version_check
 CHECK (notification_tracking_version IS NULL OR (notification_tracking_version=1 AND NOT notification_sent));
CREATE UNIQUE INDEX IF NOT EXISTS slaalerthistory_id_tenant_id_ticket_id ON public.sla_alert_histories(id,tenant_id,ticket_id);
CREATE INDEX IF NOT EXISTS ticketnotification_tenant_id_sla_alert_history_id ON public.ticket_notifications(tenant_id,sla_alert_history_id);
ALTER TABLE public.ticket_notifications ADD CONSTRAINT ticket_notification_sla_alert_tenant_work_item_fk
 FOREIGN KEY(sla_alert_history_id,tenant_id,ticket_id) REFERENCES public.sla_alert_histories(id,tenant_id,ticket_id);

CREATE FUNCTION public.preserve_sla_alert_delivery_reference() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_RELID <> 'public.ticket_notifications'::regclass OR TG_OP NOT IN ('INSERT','UPDATE') OR TG_WHEN <> 'BEFORE' OR TG_LEVEL <> 'ROW' THEN
  RAISE EXCEPTION 'invalid SLA notification reference trigger context';
 END IF;
 IF TG_OP='UPDATE' THEN
  IF NEW.sla_alert_history_id IS DISTINCT FROM OLD.sla_alert_history_id
   OR (OLD.sla_alert_history_id IS NOT NULL AND (NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.ticket_id IS DISTINCT FROM OLD.ticket_id)) THEN
   RAISE EXCEPTION 'SLA notification reference is immutable';
  END IF;
 END IF;
 IF NEW.sla_alert_history_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM public.sla_alert_histories h WHERE h.id=NEW.sla_alert_history_id
   AND h.tenant_id=NEW.tenant_id AND h.ticket_id=NEW.ticket_id AND h.notification_tracking_version=1
 ) THEN
  RAISE EXCEPTION 'SLA notification requires a matching managed alert' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER sla_alert_delivery_reference_immutable BEFORE INSERT OR UPDATE OF sla_alert_history_id,tenant_id,ticket_id ON public.ticket_notifications
FOR EACH ROW EXECUTE FUNCTION public.preserve_sla_alert_delivery_reference();
CREATE FUNCTION public.preserve_sla_alert_tracking() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_RELID <> 'public.sla_alert_histories'::regclass OR TG_OP <> 'UPDATE' OR TG_WHEN <> 'BEFORE' OR TG_LEVEL <> 'ROW' THEN
  RAISE EXCEPTION 'invalid SLA alert tracking trigger context';
 END IF;
 IF NEW.notification_tracking_version IS DISTINCT FROM OLD.notification_tracking_version
  OR (OLD.notification_tracking_version IS NOT NULL AND (NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.ticket_id IS DISTINCT FROM OLD.ticket_id OR NEW.id IS DISTINCT FROM OLD.id)) THEN
  RAISE EXCEPTION 'SLA alert tracking identity is immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER sla_alert_tracking_immutable BEFORE UPDATE OF notification_tracking_version,tenant_id,ticket_id,id ON public.sla_alert_histories
FOR EACH ROW EXECUTE FUNCTION public.preserve_sla_alert_tracking();
REVOKE ALL ON FUNCTION public.preserve_sla_alert_delivery_reference(),public.preserve_sla_alert_tracking() FROM PUBLIC;
DO $$ DECLARE permission record; BEGIN
 FOR permission IN
  SELECT DISTINCT p.oid::regprocedure AS object_name,a.grantee
  FROM pg_catalog.pg_proc p
  CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(p.proacl,pg_catalog.acldefault('f',p.proowner))) a
  WHERE p.oid IN ('public.preserve_sla_alert_delivery_reference()'::regprocedure,'public.preserve_sla_alert_tracking()'::regprocedure)
   AND a.grantee<>0 AND a.grantee<>p.proowner
 LOOP
  EXECUTE format('REVOKE ALL ON FUNCTION %s FROM %I',permission.object_name,pg_catalog.pg_get_userbyid(permission.grantee));
 END LOOP;
END $$;
`

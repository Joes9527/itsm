package migration

const emailAttachmentSourceIdentitySQL = `DO $prerequisite$
DECLARE target text;
BEGIN
 FOR target IN SELECT unnest(ARRAY['ticket_attachments','tickets','users']) LOOP
  IF to_regclass(format('%I.%I',current_schema(),target)) IS NULL THEN
   RAISE EXCEPTION 'required local % table missing; restore attachment ownership prerequisites',target;
  END IF;
 END LOOP;
END $prerequisite$;
ALTER TABLE ticket_attachments ADD COLUMN IF NOT EXISTS source_key varchar(64) NULL;
DO $history$
DECLARE invalid_ids text;
BEGIN
 SELECT string_agg(id::text,',') INTO invalid_ids FROM (
  SELECT a.id FROM ticket_attachments a
  LEFT JOIN tickets t ON t.id=a.ticket_id LEFT JOIN users u ON u.id=a.uploaded_by
  WHERE a.source_key IS NOT NULL AND (a.source_key !~ '^[0-9a-f]{64}$' OR t.id IS NULL OR u.id IS NULL OR t.tenant_id<>a.tenant_id OR u.tenant_id<>a.tenant_id)
  ORDER BY a.id LIMIT 20
 ) invalid;
 IF invalid_ids IS NOT NULL THEN RAISE EXCEPTION 'invalid inbound attachment ownership for IDs %; reconcile source history before retry',invalid_ids; END IF;
END $history$;
CREATE UNIQUE INDEX IF NOT EXISTS ticketattachment_tenant_id_source_key ON ticket_attachments(tenant_id,source_key);
CREATE OR REPLACE FUNCTION validate_email_attachment_source_owner() RETURNS trigger LANGUAGE plpgsql AS $owner$
BEGIN
 IF TG_OP='UPDATE' AND (OLD.source_key IS NOT NULL OR NEW.source_key IS NOT NULL) AND
 (OLD.source_key,OLD.tenant_id,OLD.ticket_id,OLD.uploaded_by,OLD.file_path,OLD.file_size,OLD.file_name,OLD.file_type)
 IS DISTINCT FROM
 (NEW.source_key,NEW.tenant_id,NEW.ticket_id,NEW.uploaded_by,NEW.file_path,NEW.file_size,NEW.file_name,NEW.file_type)
 THEN RAISE EXCEPTION 'inbound attachment source identity, ownership and content are immutable'; END IF;
 IF NEW.source_key IS NOT NULL THEN
  IF NEW.source_key !~ '^[0-9a-f]{64}$' THEN RAISE EXCEPTION 'invalid inbound attachment source identity'; END IF;
  IF NOT EXISTS(SELECT 1 FROM tickets WHERE id=NEW.ticket_id AND tenant_id=NEW.tenant_id)
   OR NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.uploaded_by AND tenant_id=NEW.tenant_id)
  THEN RAISE EXCEPTION 'inbound attachment source owner tenant mismatch'; END IF;
 END IF;
 RETURN NEW;
END $owner$;
DROP TRIGGER IF EXISTS email_attachment_source_owner ON ticket_attachments;
CREATE TRIGGER email_attachment_source_owner BEFORE INSERT OR UPDATE ON ticket_attachments FOR EACH ROW EXECUTE FUNCTION validate_email_attachment_source_owner();
DO $verify$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname=current_schema() AND c.relname='ticket_attachments' AND a.attname='source_key' AND NOT a.attisdropped
  AND a.atttypid='varchar'::regtype AND NOT a.attnotnull AND NOT a.atthasdef AND (a.atttypmod=-1 OR a.atttypmod>=68))
 THEN RAISE EXCEPTION 'ticket_attachments.source_key must be nullable varchar with capacity >=64 and no default'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname=current_schema() AND c.relname='ticket_attachments' AND i.indisunique AND i.indisvalid AND i.indisready
  AND i.indnatts=2 AND i.indpred IS NULL AND i.indexprs IS NULL
  AND (SELECT array_agg(a.attname::text ORDER BY k.position) FROM unnest(i.indkey) WITH ORDINALITY k(attnum,position)
   JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=k.attnum)=ARRAY['tenant_id','source_key'])
 THEN RAISE EXCEPTION 'complete tenant/source unique index required for inbound attachments'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname=current_schema() AND c.relname='ticket_attachments' AND t.tgname='email_attachment_source_owner' AND t.tgenabled='O' AND NOT t.tgisinternal)
 THEN RAISE EXCEPTION 'inbound attachment owner trigger required'; END IF;
END $verify$;
`

const emailAttachmentSourceIdentityDevelopmentResetSQL = `DO $reset$
BEGIN
 IF to_regclass(format('%I.ticket_attachments',current_schema())) IS NULL THEN RAISE EXCEPTION 'required local ticket_attachments table missing'; END IF;
 IF EXISTS(SELECT 1 FROM ticket_attachments LIMIT 1) THEN RAISE EXCEPTION 'development reset requires empty ticket_attachments; durable source references cannot be discarded'; END IF;
END $reset$;
DROP TRIGGER IF EXISTS email_attachment_source_owner ON ticket_attachments;
DROP FUNCTION IF EXISTS validate_email_attachment_source_owner();
ALTER TABLE ticket_attachments DROP COLUMN IF EXISTS source_key;
`

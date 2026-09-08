DO $reset$
BEGIN
 IF to_regclass(format('%I.ticket_attachments',current_schema())) IS NULL THEN RAISE EXCEPTION 'required local ticket_attachments table missing'; END IF;
 IF EXISTS(SELECT 1 FROM ticket_attachments LIMIT 1) THEN RAISE EXCEPTION 'development reset requires empty ticket_attachments; durable source references cannot be discarded'; END IF;
END $reset$;
DROP TRIGGER IF EXISTS email_attachment_source_owner ON ticket_attachments;
DROP FUNCTION IF EXISTS validate_email_attachment_source_owner();
ALTER TABLE ticket_attachments DROP COLUMN IF EXISTS source_key;

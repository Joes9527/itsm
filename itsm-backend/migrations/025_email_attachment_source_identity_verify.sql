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

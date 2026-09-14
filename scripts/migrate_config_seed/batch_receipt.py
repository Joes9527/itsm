"""Transaction-bound receipts for reviewed, database-specific migration artifacts."""
import hashlib
import json
import re
from pathlib import Path


def literal(value):
    return "'" + str(value).replace("'", "''") + "'"


def canonical(value):
    return json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(',', ':'))


def snapshot_expression(targets, identities=False):
    row_value = 'to_jsonb(t.id)' if identities else 'to_jsonb(t)'
    parts = [f"jsonb_build_object('table',{literal(table)},'selector',{literal(selector)},'rows',"
             f"(SELECT coalesce(jsonb_agg({row_value} ORDER BY t.id),'[]'::jsonb) FROM {table} t WHERE {selector}))"
             for table, selector in targets]
    if not parts:
        return "'[]'::jsonb"
    values = ','.join(f'({i},{part})' for i, part in enumerate(parts))
    return f'(SELECT jsonb_agg(v.object ORDER BY v.ordinal) FROM (VALUES {values}) v(ordinal,object))'


def snapshot_query(targets):
    return f"SELECT encode(sha256(convert_to(({snapshot_expression(targets)})::text,'UTF8')),'hex');"


def protect_batch(sql, batch_id, tenant_id, source, targets, context, new_objects=False, dependency_tables=()):
    required = ('database', 'schema', 'ga_revision', 'code_revision', 'actor', 'source_id',
                'expected_pre_state_sha256')
    if not context or any(not context.get(k) for k in required):
        raise ValueError('reviewed batch context is required: ' + ', '.join(required))
    if not re.fullmatch(r'[a-z_][a-z0-9_]{0,62}', context['schema']):
        raise ValueError('schema must be a lowercase SQL identifier')
    for key, length in [('ga_revision', 40), ('code_revision', 40), ('expected_pre_state_sha256', 64)]:
        if not re.fullmatch('[0-9a-f]{' + str(length) + '}', context[key]):
            raise ValueError(f'{key} must be a full lowercase digest')
    if tenant_id <= 0:
        raise ValueError('tenant_id must be positive')
    targets = sorted(set(targets))
    # Mapping digest binds the actual generated DML and selectors, not just a version label.
    mapping_digest = hashlib.sha256(canonical([sql, targets, new_objects, sorted(dependency_tables)]).encode()).hexdigest()
    source_digest = hashlib.sha256(canonical(source).encode()).hexdigest()
    ctx = literal(canonical(context)) + '::jsonb'
    objects = snapshot_expression(targets, identities=True)
    snapshot = snapshot_expression(targets)
    body = re.sub(r'\bBEGIN;', '', sql, count=1)
    body = re.sub(r'COMMIT;\s*$', '', body)
    delimiter = '$batch_' + mapping_digest + '$'
    while delimiter in body or delimiter in ctx:
        delimiter = delimiter[:-1] + '_x$'
    lock_tables = ', '.join(sorted({table for table, _ in targets} | set(dependency_tables)))
    lock = f'LOCK TABLE {lock_tables} IN SHARE ROW EXCLUSIVE MODE;' if lock_tables else ''
    untracked = ' OR '.join(f'EXISTS (SELECT 1 FROM {table} WHERE {selector})' for table, selector in targets) or 'false'
    admission_guard = (f"IF {untracked} THEN RAISE EXCEPTION 'untracked existing objects; receipt cannot be fabricated'; END IF;"
                       if new_objects else '')
    # Serialize schema bootstrap and receipt lookup before checking or taking snapshots.
    return f"""BEGIN;
SELECT pg_advisory_xact_lock(hashtextextended('config_migration_control.bootstrap',0));
DO {delimiter} BEGIN
  IF current_database() <> {literal(context['database'])} OR current_schema() <> {literal(context['schema'])} THEN
    RAISE EXCEPTION 'batch destination differs from reviewed context';
  END IF;
END {delimiter};
SET LOCAL search_path TO {context['schema']}, pg_catalog, pg_temp;
CREATE SCHEMA IF NOT EXISTS config_migration_control;
REVOKE ALL ON SCHEMA config_migration_control FROM PUBLIC;
CREATE TABLE IF NOT EXISTS config_migration_control.receipts (
  batch_id text NOT NULL, tenant_id bigint NOT NULL, context jsonb NOT NULL,
  source_sha256 text NOT NULL, mapping_sha256 text NOT NULL, object_set jsonb NOT NULL,
  post_state_sha256 text NOT NULL, committed_at timestamptz NOT NULL DEFAULT now(),
  migration_owner text NOT NULL DEFAULT current_user, PRIMARY KEY(batch_id,tenant_id)
);
REVOKE ALL ON TABLE config_migration_control.receipts FROM PUBLIC;
DO $owner$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
             WHERE n.nspname='config_migration_control' AND c.relname='receipts'
               AND pg_get_userbyid(c.relowner) <> current_user) THEN
    RAISE EXCEPTION 'receipt owner differs from migration executor';
  END IF;
END $owner$;
SELECT pg_advisory_xact_lock(hashtextextended({literal(batch_id + ':' + str(tenant_id))},0));
{lock}
DO {delimiter}
DECLARE receipt config_migration_control.receipts%ROWTYPE; state_digest text;
BEGIN
  state_digest := encode(sha256(convert_to(({snapshot})::text,'UTF8')),'hex');
  SELECT * INTO receipt FROM config_migration_control.receipts
    WHERE batch_id={literal(batch_id)} AND tenant_id={tenant_id} FOR UPDATE;
  IF FOUND THEN
    IF receipt.context IS DISTINCT FROM {ctx}
       OR receipt.source_sha256 <> {literal(source_digest)}
       OR receipt.mapping_sha256 <> {literal(mapping_digest)}
       OR receipt.object_set IS DISTINCT FROM {objects}
       OR receipt.migration_owner <> current_user
       OR receipt.post_state_sha256 <> state_digest THEN
      RAISE EXCEPTION 'batch receipt or target drift; automatic replay refused';
    END IF;
    RETURN;
  END IF;
  IF state_digest <> {literal(context['expected_pre_state_sha256'])} THEN
    RAISE EXCEPTION 'target differs from reviewed pre-state';
  END IF;
  {admission_guard}
{body}
  state_digest := encode(sha256(convert_to(({snapshot})::text,'UTF8')),'hex');
  INSERT INTO config_migration_control.receipts
    (batch_id,tenant_id,context,source_sha256,mapping_sha256,object_set,post_state_sha256)
    VALUES ({literal(batch_id)},{tenant_id},{ctx},{literal(source_digest)},
            {literal(mapping_digest)},{objects},state_digest);
END {delimiter};
COMMIT;
"""


def add_context_argument(parser):
    parser.add_argument('--batch-context', required=True, help='Reviewed destination, revisions, actor, source and pre-state digest JSON')
    parser.add_argument('--preflight-out', help='Write read-only target digest SQL for review instead of an executable batch')


def load_context(path, source_paths):
    context = json.loads(Path(path).read_text(encoding='utf-8'))
    context['source_byte_sha256'] = {name: hashlib.sha256(Path(file).read_bytes()).hexdigest()
                                     for name, file in source_paths.items()}
    return context


def write_batch(args, sql, batch_id, source, targets, source_paths, new_objects=False, dependency_tables=()):
    context = load_context(args.batch_context, source_paths)
    if args.preflight_out:
        # The query reads only the exact objects in the generated plan. Its result
        # must be reviewed and copied into expected_pre_state_sha256 before apply.
        Path(args.preflight_out).write_text(snapshot_query(sorted(set(targets))) + '\n', encoding='utf-8')
        return
    result = protect_batch(sql, batch_id, args.tenant_id, source, targets, context,
                           new_objects=new_objects, dependency_tables=dependency_tables)
    Path(args.out).write_text(result, encoding='utf-8')

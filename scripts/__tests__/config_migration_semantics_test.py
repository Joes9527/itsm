"""Real PostgreSQL regression tests using the approved disposable container/schema."""
import os
import subprocess
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'migrate_config_seed'))
from generate_seed_sql import build_dml as seed_sql
from generate_b1_sql import build_dml as b1_sql
from generate_b2_sql import build_dml as b2_sql
from generate_b4_sql import build_dml as b4_sql


class MigrationSemantics(unittest.TestCase):
    def sql(self, sql, success=True, error=None):
        command = ['docker', 'exec', '-i', os.environ['CONFIG_MIGRATION_TEST_CONTAINER'],
                   'psql', '-U', 'gb_test_owner', '-d', 'gb_review_test', '-X', '-q', '-v', 'ON_ERROR_STOP=1', '-At']
        result = subprocess.run(command, input='SET search_path TO migration_semantics_test;\n' + sql,
                                text=True, capture_output=True)
        if success:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, 'unsafe batch succeeded')
            if error:
                self.assertIn(error, result.stderr)
        return result.stdout.strip()

    def setUp(self):
        self.sql('''DROP SCHEMA IF EXISTS migration_semantics_test CASCADE; CREATE SCHEMA migration_semantics_test;
        CREATE TABLE ticket_categories(id serial PRIMARY KEY, name text, description text, code text UNIQUE,
          level int, sort_order int, is_active bool, tenant_id int, itsm_type text, default_priority text,
          sla_tier text, default_resolver text, is_user_facing bool, created_at timestamptz,
          updated_at timestamptz, parent_id int);
        CREATE TABLE ticket_templates(id serial PRIMARY KEY, name text, tenant_id int);
        CREATE TABLE field_definitions(id serial PRIMARY KEY, tenant_id int, entity_type text,
          entity_id int, name text, options jsonb, updated_at timestamptz);''')
        self.sql("""CREATE SCHEMA IF NOT EXISTS config_migration_control;
        DO $clear$ BEGIN IF to_regclass('config_migration_control.receipts') IS NOT NULL THEN
          DELETE FROM config_migration_control.receipts WHERE context->>'schema'='migration_semantics_test';
        END IF; END $clear$;""")

    def seed(self, categories):
        data = dict.fromkeys(['ticket_templates', 'sla_definitions', 'service_catalog', 'ci_types',
                             'standard_changes', 'known_errors', 'ticket_tags', 'ticket_views'], [])
        data['ticket_categories'] = categories
        return seed_sql(data, 1, 1)

    def test_b0_rejects_other_tenant_category_and_rolls_back_prior_insert(self):
        self.sql("INSERT INTO ticket_categories(code,name,tenant_id) VALUES ('TAKEN','Other',2)")
        self.sql(self.seed([{'code': 'NEW', 'name': 'New'}, {'code': 'TAKEN', 'name': 'Target'}]), False)
        self.assertEqual(self.sql('SELECT code FROM ticket_categories ORDER BY code'), 'TAKEN')

    def test_b0_rejects_changed_existing_category(self):
        self.sql("INSERT INTO ticket_categories(code,name,tenant_id) VALUES ('TAKEN','Changed',1)")
        self.sql(self.seed([{'code': 'TAKEN', 'name': 'Original'}]), False)
        self.assertEqual(self.sql('SELECT name FROM ticket_categories'), 'Changed')

    def test_b0_rejects_foreign_parent(self):
        self.sql("INSERT INTO ticket_categories(code,name,tenant_id) VALUES ('PARENT','Other',2)")
        self.sql(self.seed([{'code': 'CHILD', 'name': 'Child', 'parent_code': 'PARENT'}]), False)
        self.assertEqual(self.sql('SELECT count(*) FROM ticket_categories'), '1')

    def test_b1_rejects_foreign_category(self):
        self.sql("INSERT INTO ticket_categories(code,name,tenant_id) VALUES ('COL-MAIL','Mail',1),('ACC-LCM','Account',1),('APP-GEN','App',1)")
        self.sql("INSERT INTO ticket_categories(code,name,tenant_id) VALUES ('COL-MAIL-004','Other',2)")
        self.sql(b1_sql([], 1), False, 'category tenant or content conflict')
        self.assertEqual(self.sql('SELECT count(*) FROM ticket_categories'), '4')

    def test_b2_updates_only_template_fields(self):
        self.sql("""INSERT INTO ticket_templates VALUES (7,'Account',1);
        INSERT INTO field_definitions(tenant_id,entity_type,entity_id,name,options) VALUES
          (1,'ticket_template',7,'system','[]'),(1,'service_catalog',7,'system','[]');""")
        self.sql(b2_sql([('Account','system','New','new')], 1))
        self.assertEqual(self.sql("SELECT options FROM field_definitions WHERE entity_type='service_catalog'"), '[]')
        self.assertEqual(self.sql("SELECT options->0->>'value' FROM field_definitions WHERE entity_type='ticket_template'"), 'new')

    def test_b2_rejects_missing_field(self):
        self.sql("INSERT INTO ticket_templates VALUES (7,'Account',1)")
        self.sql(b2_sql([('Account','system','New','new')], 1), False)

    def test_b2_rejects_ambiguous_field(self):
        self.sql("""INSERT INTO ticket_templates VALUES (7,'Account',1);
        INSERT INTO field_definitions(tenant_id,entity_type,entity_id,name,options) VALUES
          (1,'ticket_template',7,'system','[]'),(1,'ticket_template',7,'system','[]');""")
        self.sql(b2_sql([('Account','system','New','new')], 1), False)

    def test_batch_retry_uses_atomic_receipt_and_rejects_target_drift(self):
        from batch_receipt import protect_batch, snapshot_query
        targets = [('ticket_categories', "tenant_id=1 AND code='A'")]
        before = self.sql(snapshot_query(targets))
        context = dict(database='gb_review_test', schema='migration_semantics_test',
                       ga_revision='a'*40, code_revision='b'*40, actor='review-test',
                       source_id='isolated-fixture', expected_pre_state_sha256=before)
        batch = protect_batch(self.seed([{'code':'A','name':'Original'}]), 'B0-test', 1,
                              {'fixture': 1}, targets, context)
        self.sql(batch)
        # Simulate lost external acknowledgement: execute the exact artifact again.
        self.sql(batch)
        self.assertEqual(self.sql("SELECT count(*) FROM ticket_categories"), '1')
        self.assertEqual(self.sql("SELECT count(*) FROM config_migration_control.receipts WHERE batch_id='B0-test'"), '1')
        self.sql("UPDATE ticket_categories SET name='Operator edit'")
        self.sql(batch, False)
        self.assertEqual(self.sql('SELECT name FROM ticket_categories'), 'Operator edit')

    def test_batch_source_mapping_and_destination_changes_are_rejected(self):
        from batch_receipt import protect_batch, snapshot_query
        targets = [('ticket_categories', "tenant_id=1 AND code='A'")]
        context = dict(database='gb_review_test', schema='migration_semantics_test',
                       ga_revision='a'*40, code_revision='b'*40, actor='review-test',
                       source_id='isolated-fixture', expected_pre_state_sha256=self.sql(snapshot_query(targets)))
        dml = self.seed([{'code':'A','name':'Original'}])
        self.sql(protect_batch(dml, 'B0-test', 1, {'source':1}, targets, context))
        for source, body, ctx in [({'source':2},dml,context), ({'source':1},dml+'\n-- changed mapping',context),
                                  ({'source':1},dml,{**context,'database':'wrong_database'})]:
            self.sql(protect_batch(body, 'B0-test', 1, source, targets, ctx), False)
        self.assertEqual(self.sql('SELECT name FROM ticket_categories'), 'Original')

    def test_batch_preserves_dollar_delimiters_in_configuration_text(self):
        from batch_receipt import protect_batch, snapshot_query
        targets = [('ticket_categories', "tenant_id=1 AND code='A'")]
        context = dict(database='gb_review_test', schema='migration_semantics_test',
                       ga_revision='a'*40, code_revision='b'*40, actor='review-test',
                       source_id='isolated-fixture', expected_pre_state_sha256=self.sql(snapshot_query(targets)))
        self.sql(protect_batch(self.seed([{'code':'A','name':'$batch$ text'}]), 'B0-test', 1, {}, targets, context))
        self.assertEqual(self.sql('SELECT name FROM ticket_categories'), '$batch$ text')

    def test_first_insert_batch_does_not_adopt_existing_objects_without_receipt(self):
        from batch_receipt import protect_batch, snapshot_query
        self.sql("INSERT INTO ticket_categories(code,name,tenant_id) VALUES ('A','Untracked',1)")
        targets = [('ticket_categories', "tenant_id=1 AND code='A'")]
        context = dict(database='gb_review_test', schema='migration_semantics_test',
                       ga_revision='a'*40, code_revision='b'*40, actor='review-test',
                       source_id='isolated-fixture', expected_pre_state_sha256=self.sql(snapshot_query(targets)))
        batch = protect_batch('BEGIN; COMMIT;', 'B0-test', 1, {}, targets, context, new_objects=True)
        self.sql(batch, False)
        self.assertEqual(self.sql('SELECT name FROM ticket_categories'), 'Untracked')

    def test_snapshot_handles_full_seed_scale(self):
        from batch_receipt import snapshot_query
        targets = [('ticket_categories', f"code='category-{i}'") for i in range(185)]
        self.assertRegex(self.sql(snapshot_query(targets)), r'^[a-f0-9]{64}$')

    def test_batch_receipt_rolls_back_with_write_failure(self):
        from batch_receipt import protect_batch, snapshot_query
        targets = [('ticket_categories', "tenant_id=1 AND code='A'")]
        context = dict(database='gb_review_test', schema='migration_semantics_test',
                       ga_revision='a'*40, code_revision='b'*40, actor='review-test',
                       source_id='isolated-fixture', expected_pre_state_sha256=self.sql(snapshot_query(targets)))
        batch = protect_batch("BEGIN; INSERT INTO ticket_categories(code,tenant_id) VALUES ('A',1); SELECT 1/0; COMMIT;",
                              'failed-test', 1, {}, targets, context)
        self.sql(batch, False)
        self.assertEqual(self.sql('SELECT count(*) FROM ticket_categories'), '0')
        if self.sql("SELECT to_regclass('config_migration_control.receipts')"):
            self.assertEqual(self.sql("SELECT count(*) FROM config_migration_control.receipts WHERE batch_id='failed-test'"), '0')

    def test_b4_leaves_new_sla_calendar_untouched(self):
        from batch_receipt import protect_batch, snapshot_query
        from generate_b4_sql import batch_targets
        self.sql("""CREATE TABLE sla_definitions(id serial PRIMARY KEY,name text,service_type text,priority text,
          tenant_id int,business_hours jsonb,updated_at timestamptz);
        INSERT INTO sla_definitions(name,service_type,priority,tenant_id,business_hours) VALUES
        ('Incident-P0-紧急','incident','urgent',1,'{}'),('Incident-P1-高','incident','high',1,'{}'),
        ('Incident-P2-中','incident','medium',1,'{}'),('Incident-P3-低','incident','low',1,'{}'),
        ('ServiceRequest-标准','service_request','medium',1,'{}'),('Change-普通','change','medium',1,'{}'),
        ('Change-紧急','change','high',1,'{}'),('Operator SLA','incident','low',1,'{"custom":true}');""")
        targets = batch_targets(1)
        context = dict(database='gb_review_test', schema='migration_semantics_test',
                       ga_revision='a'*40, code_revision='b'*40, actor='review-test',
                       source_id='isolated-fixture', expected_pre_state_sha256=self.sql(snapshot_query(sorted(targets))))
        batch = protect_batch(b4_sql({'start_time':'09:00'}, 1), 'B4-test', 1, {}, targets, context)
        self.sql(batch)
        self.sql("INSERT INTO sla_definitions(name,tenant_id,business_hours) VALUES ('Later SLA',1,'{\"later\":true}')")
        self.sql(batch)
        self.assertEqual(self.sql("SELECT business_hours->>'custom' FROM sla_definitions WHERE name='Operator SLA'"), 'true')
        self.assertEqual(self.sql("SELECT business_hours->>'later' FROM sla_definitions WHERE name='Later SLA'"), 'true')
        self.assertEqual(self.sql("SELECT count(*) FROM sla_definitions WHERE business_hours->>'start_time'='09:00'"), '7')

    def test_batch_uses_reviewed_schema_even_with_temporary_namesake(self):
        from batch_receipt import protect_batch, snapshot_query
        targets = [('ticket_categories', "tenant_id=1 AND code='A'")]
        context = dict(database='gb_review_test', schema='migration_semantics_test',
                       ga_revision='a'*40, code_revision='b'*40, actor='review-test',
                       source_id='isolated-fixture', expected_pre_state_sha256=self.sql(snapshot_query(targets)))
        batch = protect_batch(self.seed([{'code':'A','name':'Real'}]), 'B0-test', 1, {}, targets, context)
        self.sql('CREATE TEMP TABLE ticket_categories (LIKE migration_semantics_test.ticket_categories INCLUDING ALL);' + batch)
        self.assertEqual(self.sql('SELECT name FROM migration_semantics_test.ticket_categories'), 'Real')


if __name__ == '__main__':
    if os.environ.get('CONFIG_MIGRATION_TEST_CONTAINER') != 'gb-remediation-test-pg-20260914':
        raise SystemExit('CONFIG_MIGRATION_TEST_CONTAINER must name the approved isolated container')
    unittest.main()

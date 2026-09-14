"""Real PostgreSQL regression tests; CONFIG_MIGRATION_TEST_DSN must be isolated."""
import os
import subprocess
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'migrate_config_seed'))
from generate_seed_sql import build_sql as seed_sql
from generate_b1_sql import build_sql as b1_sql
from generate_b2_sql import build_sql as b2_sql


class MigrationSemantics(unittest.TestCase):
    def sql(self, sql, success=True):
        command = ['docker', 'exec', '-i', os.environ['CONFIG_MIGRATION_TEST_CONTAINER'],
                   'psql', '-U', 'gb_test_owner', '-d', 'gb_review_test', '-X', '-q', '-v', 'ON_ERROR_STOP=1', '-At']
        result = subprocess.run(command, input='SET search_path TO migration_semantics_test;\n' + sql,
                                text=True, capture_output=True)
        if success:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, 'unsafe batch succeeded')
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
        self.sql("INSERT INTO ticket_categories(code,name,tenant_id) VALUES ('COL-MAIL-004','Other',2)")
        self.sql(b1_sql([], 1), False)
        self.assertEqual(self.sql('SELECT count(*) FROM ticket_categories'), '1')

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


if __name__ == '__main__':
    if os.environ.get('CONFIG_MIGRATION_TEST_CONTAINER') != 'gb-remediation-test-pg-20260914':
        raise SystemExit('CONFIG_MIGRATION_TEST_CONTAINER must name the approved isolated container')
    unittest.main()

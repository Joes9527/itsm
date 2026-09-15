"""Fail-closed checks for disposable GA preparation orchestration."""
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch
import tempfile
import secrets
import io
from contextlib import redirect_stdout

MODULE = Path(__file__).resolve().parents[1] / 'ga-controlled-preparation.py'
spec = importlib.util.spec_from_file_location('ga_preparation', MODULE)
ga = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ga)


class Fixture:
    def __init__(self):
        self.calls = []
        self.initial = (1, ga.PREPARATION_BOUNDARY)
        self.restored = b'021|checksum\n'
        self.prepare_error = None
        self.evidence = None

    def initial_bootstrap(self):
        self.calls.append('initial')
        return self.initial

    def provision_roles(self):
        self.calls.append('roles')

    def inventory(self):
        self.calls.append('inventory')
        return {'InventoryDigest': 'actual-inventory', 'LedgerDigest': 'actual-ledger'}

    def backup(self):
        self.calls.append('backup')
        return b'actual database dump'

    def restore(self, backup):
        assert backup == b'actual database dump'
        self.calls.append('restore')
        return self.restored

    def ledger(self):
        self.calls.append('ledger')
        return b'021|checksum\n'

    def application_digest(self):
        return 'a' * 64

    def operator(self):
        return 'app'

    def apply_preparation(self, evidence):
        self.calls.append('prepare')
        self.evidence = evidence
        if self.prepare_error:
            raise self.prepare_error

    def finish_bootstrap(self):
        self.calls.append('finish')

    def verify_prepared(self):
        self.calls.append('verify')


class PreparationTests(unittest.TestCase):
    def test_real_artifact_digests_and_order(self):
        fixture = Fixture()
        summary = ga.prepare_database(fixture, 'ga-test')
        self.assertEqual(set(summary), {'runId', 'applicationDigest', 'backupDigest', 'restoreReportDigest', 'preparationReceipts', 'retirementReceipts', 'finalInitializerSucceeded'})
        self.assertEqual(summary['preparationReceipts'], 1)
        self.assertEqual(summary['retirementReceipts'], 0)
        self.assertTrue(summary['finalInitializerSucceeded'])
        self.assertEqual(fixture.calls, ['initial', 'roles', 'inventory', 'backup', 'restore', 'ledger', 'prepare', 'finish', 'verify'])
        self.assertEqual(fixture.evidence['BackupDigest'], ga.sha256(b'actual database dump'))
        self.assertEqual(fixture.evidence['RestoreReportDigest'], ga.sha256(fixture.restored))
        self.assertEqual(fixture.evidence['InventoryDigest'], 'actual-inventory')

    def test_unexpected_initial_failure_stops_before_roles_or_backup(self):
        for initial in [(0, ga.PREPARATION_BOUNDARY), (1, 'connection refused'), (1, 'runtime requires migration 999_unknown')]:
            with self.subTest(initial=initial):
                fixture = Fixture()
                fixture.initial = initial
                with self.assertRaises(RuntimeError):
                    ga.prepare_database(fixture, 'ga-test')
                self.assertEqual(fixture.calls, ['initial'])

    def test_restore_mismatch_never_prepares_or_starts_runtime(self):
        fixture = Fixture()
        fixture.restored = b'wrong ledger'
        with self.assertRaisesRegex(RuntimeError, 'restore ledger'):
            ga.prepare_database(fixture, 'ga-test')
        self.assertNotIn('prepare', fixture.calls)
        self.assertNotIn('finish', fixture.calls)

    def test_preparation_failure_does_not_resume_bootstrap(self):
        fixture = Fixture()
        fixture.prepare_error = RuntimeError('evidence rejected')
        with self.assertRaisesRegex(RuntimeError, 'evidence rejected'):
            ga.prepare_database(fixture, 'ga-test')
        self.assertNotIn('finish', fixture.calls)
        self.assertNotIn('verify', fixture.calls)


class IsolationTests(unittest.TestCase):
    def source(self):
        return {
            'name': 'shared-dev',
            'services': {name: {'environment': {}, 'container_name': 'shared-' + name,
                                'ports': [{'target': 5432, 'published': '5432'}],
                                'volumes': []} for name in ga.CORE},
            'networks': {'default': {'name': 'shared-network'}},
            'volumes': {'pg': {'name': 'shared-data'}},
        }

    def test_every_resource_is_owned_and_only_health_ports_are_published(self):
        source = self.source()
        config = ga.isolated_config(source, 'ga-preparation-test', Path('/private/mounted'),
                                    dict(app='app-secret', system='system-secret', inspect='inspect-secret'))
        ga.assert_owned_config(config, 'ga-preparation-test')
        self.assertEqual(source['services']['postgres']['container_name'], 'shared-postgres')
        self.assertEqual(config['volumes']['pg']['name'], 'ga-preparation-test_pg')
        self.assertEqual(config['networks']['default']['name'], 'ga-preparation-test_default')
        for name in ('itsm-init', 'itsm-backend'):
            self.assertNotIn('INTAKE_IDENTITY_CONFIG_FILE', config['services'][name]['environment'])
        for name in ('postgres', 'redis', 'minio', 'itsm-init'):
            self.assertEqual(config['services'][name]['ports'], [])

    def test_attachment_storage_uses_private_minio_sdk_endpoint(self):
        source = self.source()
        source['services']['itsm-backend']['environment']['MINIO_ENDPOINT'] = 'http://minio:9000'
        config = ga.isolated_config(source, 'ga-preparation-test', Path('/private'),
                                    dict(app='a', system='b', inspect='c'))
        self.assertEqual(config['services']['itsm-backend']['environment']['MINIO_ENDPOINT'], 'minio:9000')
        self.assertIn('minio', config['services'])
        self.assertIn('  use_ssl: false', (MODULE.parents[1] / 'itsm-backend' / 'config.yaml').read_text())

    def test_external_resource_rejected_before_creation_and_cleanup(self):
        for kind in ('volumes', 'networks'):
            source = self.source()
            next(iter(source[kind].values()))['external'] = True
            with self.assertRaisesRegex(ValueError, 'External'):
                ga.isolated_config(source, 'ga-preparation-test', Path('/private'), {})
            with self.assertRaises(RuntimeError):
                ga.assert_owned_config(source, 'ga-preparation-test')

    def test_cleanup_refuses_a_foreign_container_even_under_own_project(self):
        source = self.source()
        config = ga.isolated_config(source, 'ga-preparation-test', Path('/private'),
                                    dict(app='a', system='b', inspect='c'))
        config['services']['postgres']['container_name'] = 'shared-postgres'
        with self.assertRaisesRegex(RuntimeError, 'unowned container'):
            ga.assert_owned_config(config, 'ga-preparation-test')

    def test_artifact_logs_redact_generated_secrets_and_connection_identity(self):
        config = self.source()
        inspection_password = secrets.token_hex(24)
        inspection_dsn = 'postgresql://inspect:' + inspection_password + '@postgres/itsm'
        config['services']['itsm-backend']['environment'] = {
            'DB_PASSWORD': 'sensitive-password',
            'ITSM_MIGRATION_INSPECTION_DSN': inspection_dsn,
            'JWT_SECRET': 'sensitive-signing-key',
        }
        raw = ('sensitive-password ' + inspection_dsn + ' sensitive-signing-key visible failure').encode()
        redacted = ga.redact_logs(config, raw)
        self.assertNotIn('sensitive-password', redacted)
        self.assertNotIn(inspection_password, redacted)
        self.assertNotIn(inspection_dsn, redacted)
        self.assertNotIn('sensitive-signing-key', redacted)
        self.assertIn('visible failure', redacted)

class SummaryPublicationTests(unittest.TestCase):
    def test_final_initializer_failure_never_publishes_success_summary(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            private = root / 'private'
            private.mkdir()
            environment_file = root / 'github-env'

            class FailedFinalInit:
                def __init__(self, repo, folder, project):
                    self.config = folder / 'compose.json'

                def create(self):
                    pass

                def provision_storage(self):
                    pass

                def compose(self, *args):
                    raise RuntimeError('final initializer failed')

            with patch.object(ga, '__file__', str(root / 'scripts' / 'ga-controlled-preparation.py')), \
                    patch.object(ga, 'ComposeFixture', FailedFinalInit), \
                    patch.object(ga, 'prepare_database', return_value={'finalInitializerSucceeded': True}), \
                    patch.object(ga.tempfile, 'mkdtemp', return_value=str(private)), \
                    patch.dict(ga.os.environ, {'GITHUB_ACTIONS': 'true', 'GITHUB_ENV': str(environment_file)}), \
                    patch.object(ga.sys, 'argv', ['ga-controlled-preparation.py', 'prepare']):
                with self.assertRaisesRegex(RuntimeError, 'final initializer failed'):
                    ga.main()
            self.assertFalse((root / 'ga-gate-preparation-summary.json').exists())

class RuntimeGrantTests(unittest.TestCase):
    def test_post_preparation_does_not_reopen_canonical_function_acl(self):
        with tempfile.TemporaryDirectory() as temporary:
            folder = Path(temporary)
            fixture = ga.ComposeFixture(folder, folder, 'ga-preparation-test')
            with patch.object(fixture, 'migrate') as migrate, \
                    patch.object(fixture, 'init_command', return_value=ga.subprocess.CompletedProcess([], 0)), \
                    patch.object(fixture, 'sql') as sql:
                fixture.finish_bootstrap()
            fixture.log.close()
            migrate.assert_called_once_with('-up')
            grants = '\n'.join(call.args[0] for call in sql.call_args_list)
            self.assertNotIn('GRANT EXECUTE ON ALL FUNCTIONS', grants)
            self.assertNotIn('ON ALL TABLES', grants)
            self.assertNotIn('ON ALL SEQUENCES', grants)
            self.assertIn('REVOKE INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER ON schema_migrations FROM ga_app;', grants)
            self.assertIn('REVOKE ALL ON work_item_migration_evidence FROM ga_app;', grants)
            self.assertIn('GRANT SELECT ON public.execution_scopes,public.execution_scope_members,public.execution_runtime_bindings,public.execution_tool_invocations TO ga_app;', grants)
            self.assertIn("VALUES ('ga_app','ga-preparation-test','standard')", grants)

    def test_runtime_config_uses_same_isolated_standard_identity_without_capabilities(self):
        with tempfile.TemporaryDirectory() as temporary:
            folder = Path(temporary)
            fixture = ga.ComposeFixture(MODULE.parents[1], folder, 'ga-preparation-test')
            fixture.write_runtime_config()
            fixture.log.close()
            generated = (fixture.public / 'config.yaml').read_text()
            original = (MODULE.parents[1] / 'itsm-backend' / 'config.yaml').read_text()
            self.assertTrue(generated.startswith(original))
            self.assertIn('mode: standard', generated)
            self.assertIn('deployment_id: ga-preparation-test', generated)
            self.assertIn('capabilities: {}', generated)
            self.assertIn('scopes: []', generated)
            config = ga.isolated_config(IsolationTests().source(), fixture.project, fixture.public, fixture.passwords)
            for name in ('itsm-init', 'itsm-backend'):
                self.assertIn({'type': 'bind', 'source': str(fixture.public / 'config.yaml'), 'target': '/app/config.yaml', 'read_only': True}, config['services'][name]['volumes'])

class StorageProvisioningTests(unittest.TestCase):
    def test_failed_bucket_provisioning_never_starts_runtime_or_publishes_summary(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            private = root / 'private'
            private.mkdir()
            with patch.object(ga, '__file__', str(root / 'scripts' / 'ga-controlled-preparation.py')), \
                    patch.object(ga, 'ComposeFixture') as fixture_type, \
                    patch.object(ga, 'prepare_database', return_value={'finalInitializerSucceeded': True}), \
                    patch.object(ga.tempfile, 'mkdtemp', return_value=str(private)), \
                    patch.dict(ga.os.environ, {'GITHUB_ACTIONS': 'true', 'GITHUB_ENV': str(root / 'github-env')}), \
                    patch.object(ga.sys, 'argv', ['ga-controlled-preparation.py', 'prepare']):
                fixture_type.return_value.config = private / 'compose.json'
                fixture_type.return_value.provision_storage.side_effect = RuntimeError('bucket denied')
                with self.assertRaisesRegex(RuntimeError, 'bucket denied'):
                    ga.main()
                fixture_type.return_value.compose.assert_not_called()
            self.assertFalse((root / 'ga-gate-preparation-summary.json').exists())

    def test_provisioning_reuses_owned_runtime_configuration_and_fails_closed(self):
        with tempfile.TemporaryDirectory() as temporary:
            folder = Path(temporary)
            fixture = ga.ComposeFixture(folder, folder, 'ga-preparation-test')
            with patch.object(fixture, 'compose', side_effect=RuntimeError('bucket denied')) as compose:
                with self.assertRaisesRegex(RuntimeError, 'bucket denied'):
                    fixture.provision_storage()
            fixture.log.close()
            compose.assert_called_once_with('run', '--rm', '--no-deps', '-T', '--entrypoint', '/ga/provision-minio', 'itsm-backend')

class FailureDiagnosticsTests(unittest.TestCase):
    def test_removed_initializer_stderr_is_redacted_and_stdout_never_published(self):
        with tempfile.TemporaryDirectory() as temporary:
            folder = Path(temporary)
            fixture = ga.ComposeFixture(folder, folder, 'ga-preparation-test')
            config = ga.isolated_config(IsolationTests().source(), fixture.project, fixture.public,
                                        dict(app='fixture-password', system='system-password', inspect='inspect-password'))
            fixture.config.write_text(ga.json.dumps(config))
            failure = ga.subprocess.CompletedProcess([], 1, stdout=b'BINARY-DUMP-STDOUT CONFIG-STDOUT SQL-ROW-STDOUT',
                stderr=('initializer preparation failed: ' + config['services']['itsm-backend']['environment']['DATABASE_URL'] + ' fixture-password').encode())
            with patch.object(ga.subprocess, 'run', return_value=failure):
                with self.assertRaises(RuntimeError):
                    fixture.compose('run', '--rm', 'itsm-init')
            fixture.log.close()
            output = io.StringIO()
            with patch.dict(ga.os.environ, {'GITHUB_ACTIONS': 'true', 'GA_PREPARATION_CONFIG': str(fixture.config), 'GA_PREPARATION_PROJECT': fixture.project}), \
                    patch.object(ga.sys, 'argv', ['ga-controlled-preparation.py', 'logs']), \
                    patch.object(ga.subprocess, 'run', return_value=ga.subprocess.CompletedProcess([], 0, stdout=b'container logs', stderr=b'')), \
                    redirect_stdout(output):
                ga.main()
            public = output.getvalue()
            self.assertIn('initializer preparation failed', public)
            self.assertNotIn('fixture-password', public)
            self.assertNotIn('postgresql://', public)
            self.assertNotIn('BINARY-DUMP-STDOUT', public)
            self.assertNotIn('CONFIG-STDOUT', public)
            self.assertNotIn('SQL-ROW-STDOUT', public)

    def test_failed_cli_build_preserves_only_stderr(self):
        with tempfile.TemporaryDirectory() as temporary:
            folder = Path(temporary)
            fixture = ga.ComposeFixture(folder, folder, 'ga-preparation-test')
            failure = ga.subprocess.CompletedProcess([], 1, stdout=b'UNRELATED-BUILD-STDOUT', stderr=b'cmd/migrate/main.go:42: compile error')
            with patch.object(ga.subprocess, 'run', return_value=failure):
                with self.assertRaises(RuntimeError):
                    fixture.command(['go', 'build'], cwd=folder)
            fixture.log.close()
            retained = (folder / 'private-command-errors.log').read_bytes()
            self.assertIn(b'compile error', retained)
            self.assertNotIn(b'UNRELATED-BUILD-STDOUT', retained)

if __name__ == '__main__':
    unittest.main()
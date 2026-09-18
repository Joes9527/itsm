#!/usr/bin/env python3
"""Provision a disposable GA database through the existing controlled P CLI.

This is CI fixture orchestration, never an application migration entrypoint.
Private generated configuration and evidence stay outside uploaded artifacts.
"""
import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import socket
import subprocess
import sys
import tempfile
import uuid
import urllib.parse

PREPARATION_BOUNDARY = 'Initialization failed: run canonical schema bootstrap: runtime migration admission: runtime requires migration 037_work_item_structure_preparation'
CORE = ('postgres', 'redis', 'minio', 'itsm-init', 'itsm-backend', 'itsm-frontend')


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def prepare_database(driver, run_id):
    code, output = driver.initial_bootstrap()
    # Only the expected final initializer error permits controlled preparation.
    # A connection/schema/seed error (or a successful initializer) must stop here.
    if code == 0 or not output.rstrip().endswith(PREPARATION_BOUNDARY):
        raise RuntimeError('Fresh initializer did not stop at the exact P037 boundary')
    driver.provision_roles()
    inventory = driver.inventory()
    backup = driver.backup()
    restored_ledger = driver.restore(backup)
    if restored_ledger != driver.ledger():
        raise RuntimeError('Pre-P restore ledger differs from the source')
    evidence = dict(inventory, CatalogRevision='workitem-controlled-retirement-v1',
                    ApplicationDigest=driver.application_digest(), BackupDigest=sha256(backup),
                    RestoreReportDigest=sha256(restored_ledger), Operator=driver.operator(),
                    ChangeRecord=run_id)
    driver.apply_preparation(evidence)
    driver.finish_bootstrap()
    driver.verify_prepared()
    return {'runId': run_id, 'applicationDigest': evidence['ApplicationDigest'],
            'backupDigest': evidence['BackupDigest'], 'restoreReportDigest': evidence['RestoreReportDigest'],
            'preparationReceipts': 1, 'retirementReceipts': 0, 'finalInitializerSucceeded': True}


def isolated_config(source, project, public, passwords):
    """Keep the original service/build contracts while isolating every resource."""
    if not re.fullmatch(r'ga-preparation-[a-z0-9-]+', project):
        raise ValueError('Invalid disposable project identity')
    config = copy.deepcopy(source)
    config['name'] = project
    config['services'] = {name: config['services'][name] for name in CORE}
    for kind in ('volumes', 'networks'):
        for name, resource in config.get(kind, {}).items():
            if resource.get('external'):
                raise ValueError('External resources are forbidden in disposable GA')
            resource['name'] = project + '_' + name
            resource.setdefault('labels', {})['itsm.ga.preparation'] = project
    for name, service in config['services'].items():
        service['container_name'] = project + '-' + name
        service.setdefault('labels', {})['itsm.ga.preparation'] = project
        # Dependencies are addressed over the private Compose network.
        service['ports'] = []
        if name in ('itsm-backend', 'itsm-frontend'):
            target, published = (8090, 8090) if name == 'itsm-backend' else (3000, 3010)
            service['ports'] = [{'target': target, 'published': str(published), 'host_ip': '127.0.0.1', 'protocol': 'tcp'}]
        # Host binds in the development stack must not reach shared logs/uploads.
        for mount in service.get('volumes', []):
            if mount['type'] == 'bind':
                if mount['target'] not in ('/app/logs', '/app/uploads'):
                    raise ValueError('Unreviewed development bind mount')
                mount['source'] = str(public / name / Path(mount['target']).name)
        if name in ('itsm-init', 'itsm-backend'):
            service.setdefault('volumes', []).append({'type': 'bind', 'source': str(public), 'target': '/ga', 'read_only': True})
            service['volumes'].append({'type': 'bind', 'source': str(public / 'config.yaml'), 'target': '/app/config.yaml', 'read_only': True})
            env = service['environment']
            env['ITSM_MIGRATION_CONTROL_FILE'] = '/ga/control.json'
            env['LLM_PROVIDER'] = 'local'
            if name == 'itsm-backend':
                # minio-go takes host:port; the base config selects non-TLS.
                env['MINIO_ENDPOINT'] = 'minio:9000'
                env.update(DB_USER='ga_app', DB_PASSWORD=passwords['app'],
                           DATABASE_URL='postgresql://ga_app:' + passwords['app'] + '@postgres:5432/itsm?sslmode=disable',
                           DB_SYSTEM_ROLE_USER='ga_system', DB_SYSTEM_ROLE_PASSWORD=passwords['system'], RLS_MODE='enforce',
                           ITSM_MIGRATION_INSPECTION_DSN='postgresql://ga_inspect:' + passwords['inspect'] + '@postgres:5432/itsm?sslmode=disable&search_path=public')
    return config


class ComposeFixture:
    def __init__(self, repo, folder, project):
        self.repo, self.folder, self.project = repo, folder, project
        self.public = folder / 'mounted'
        self.public.mkdir(mode=0o755)
        self.passwords = {name: secrets.token_hex(24) for name in ('app', 'system', 'inspect')}
        self.config = folder / 'compose.json'
        self.log = (folder / 'private-setup.log').open('wb')
        self.base = ['docker', 'compose', '--project-name', project, '-f', str(self.config), '--profile', 'dev']
        self.control = {'DeploymentID': project, 'InspectionRole': 'ga_inspect',
                        'ReviewedGrants': [{'Role': 'ga_app', 'Table': table, 'Privileges': ['SELECT', 'INSERT', 'UPDATE', 'DELETE']}
                                           for table in ('tickets', 'incidents', 'problems', 'changes')]}

    def command(self, argv, *, data=None, checked=True, env=None, cwd=None):
        result = subprocess.run(argv, cwd=cwd or self.repo, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)
        self.log.write(result.stdout + result.stderr)
        self.log.flush()
        if result.returncode:
            # run --rm removes the failing container. Retain stderr separately
            # from dump/config/query stdout, and redact only at publication.
            with (self.folder / 'private-command-errors.log').open('ab') as errors:
                errors.write(result.stderr + b'\n')
        if checked and result.returncode:
            # Raw command output can contain DSNs or SQL; keep it private.
            raise RuntimeError('Disposable GA command failed; private diagnostics retained')
        return result

    def compose(self, *args, **kwargs):
        return self.command([*self.base, *args], **kwargs)

    def write_json(self, name, value):
        path = self.public / name
        path.write_text(json.dumps(value))
        path.chmod(0o644)  # Outer host directory is 0700; container app reads mount.

    def write_runtime_config(self):
        source = (self.repo / 'itsm-backend' / 'config.yaml').read_text()
        if re.search(r'^execution\s*:', source, re.MULTILINE):
            raise RuntimeError('Base execution configuration changed; review disposable override')
        if not re.fullmatch(r'ga-preparation-[a-z0-9-]+', self.project):
            raise RuntimeError('Invalid execution deployment identity')
        # Keep the real loader and base settings; explicitly disable extensions.
        path = self.public / 'config.yaml'
        path.write_text(source + '\nexecution:\n  mode: standard\n  deployment_id: ' + self.project +
                        '\n  scopes: []\n  capabilities: {}\n  connector_targets: []\n')
        path.chmod(0o644)

    def create(self):
        # A unique project is not enough if an explicitly named resource exists.
        for kind in ('container', 'volume', 'network'):
            result = self.command(['docker', kind, 'ls', *(['-a'] if kind == 'container' else []), '--format', '{{.Name}}' if kind != 'container' else '{{.Names}}'])
            if any(self.project in line for line in result.stdout.decode().splitlines()):
                raise RuntimeError('Disposable resource identity already exists')
        for port in (8090, 3010):
            with socket.socket() as probe:
                probe.bind(('127.0.0.1', port))
        raw = self.command(['docker', 'compose', '-f', 'docker-compose.dev.yml', '--profile', 'dev', 'config', '--format', 'json'])
        config = isolated_config(json.loads(raw.stdout), self.project, self.public, self.passwords)
        for service in config['services'].values():
            for mount in service.get('volumes', []):
                if mount['type'] == 'bind' and mount['target'] in ('/app/logs', '/app/uploads'):
                    path = Path(mount['source'])
                    path.mkdir(parents=True, exist_ok=True)
                    path.chmod(0o777)  # Only this disposable container's writable directory.
        self.config.write_text(json.dumps(config))
        self.config.chmod(0o600)
        self.write_json('control.json', self.control)
        self.write_runtime_config()
        env = dict(os.environ, CGO_ENABLED='0', GOOS='linux', GOARCH='amd64')
        self.command(['go', 'build', '-tags', 'migrate', '-o', str(self.public / 'migrate'), './cmd/migrate'], cwd=self.repo / 'itsm-backend', env=env)
        (self.public / 'migrate').chmod(0o755)
        self.command(['go', 'build', '-o', str(self.public / 'provision-minio'), str(self.repo / 'scripts/fixtures/ga-minio-provision/main.go')], cwd=self.repo / 'itsm-backend', env=env)
        (self.public / 'provision-minio').chmod(0o755)
        self.compose('build', 'itsm-init', 'itsm-backend', 'itsm-frontend')
        self.compose('up', '-d', '--wait', 'postgres', 'redis', 'minio')
        # Refuse a nonempty target before the initializer can write anything.
        count = self.sql("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relkind IN ('r','p','v','m','f');")
        if count.strip() != b'0':
            raise RuntimeError('GA preparation requires an empty disposable database')

    def sql(self, sql, database='itsm'):
        return self.compose('exec', '-T', 'postgres', 'psql', '-U', 'itsm_user', '-d', database, '-v', 'ON_ERROR_STOP=1', '-Atq', data=sql.encode()).stdout

    def init_command(self, *, seed):
        return self.compose('run', '--rm', '--no-deps', '-T', '-e', 'ITSM_AUTO_SEED=' + str(seed).lower(), 'itsm-init', checked=False)

    def initial_bootstrap(self):
        result = self.init_command(seed=False)
        return result.returncode, (result.stdout + result.stderr).decode(errors='replace')

    def provision_roles(self):
        self.sql("""
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE CREATE ON DATABASE itsm FROM PUBLIC;
REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA public FROM PUBLIC;
CREATE ROLE ga_app LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT PASSWORD '%s';
GRANT CONNECT ON DATABASE itsm TO ga_app;
GRANT USAGE ON SCHEMA public TO ga_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO ga_app;
GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO ga_app;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO ga_app;
CREATE ROLE ga_system LOGIN NOSUPERUSER BYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT PASSWORD '%s';
GRANT CONNECT ON DATABASE itsm TO ga_system;
GRANT USAGE ON SCHEMA public TO ga_system;
GRANT SELECT ON users,tenants,msp_allocations,process_callback_outboxes,external_identities,connector_configs TO ga_system;
GRANT SELECT,UPDATE ON outbox_events,ticket_notifications TO ga_system;
GRANT INSERT ON audit_logs TO ga_system;
GRANT SELECT(id) ON audit_logs TO ga_system;
GRANT USAGE ON SEQUENCE audit_logs_id_seq TO ga_system;
CREATE ROLE ga_inspect LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT PASSWORD '%s';
""" % (self.passwords['app'], self.passwords['system'], self.passwords['inspect']))

    def migrate(self, *args):
        return self.compose('run', '--rm', '--no-deps', '-T', '--entrypoint', '/ga/migrate', 'itsm-init', *args).stdout

    def inventory(self):
        return json.loads(self.migrate('-prepare-workitem', '-dry-run'))

    def backup(self):
        # Dump bytes and evidence are private; never emit them as job output.
        return self.compose('exec', '-T', 'postgres', 'pg_dump', '-U', 'itsm_user', '-d', 'itsm', '-Fc').stdout

    def restore(self, backup):
        self.compose('exec', '-T', 'postgres', 'createdb', '-U', 'itsm_user', 'ga_restore_p')
        self.compose('exec', '-T', 'postgres', 'pg_restore', '-U', 'itsm_user', '-d', 'ga_restore_p', '--exit-on-error', data=backup)
        return self.ledger('ga_restore_p')

    def ledger(self, database='itsm'):
        return self.sql('SELECT version,checksum FROM schema_migrations ORDER BY version;', database)

    def application_digest(self):
        output = self.compose('run', '--rm', '--no-deps', '-T', '--entrypoint', 'sha256sum', 'itsm-init', '/app/main').stdout.decode().split()
        if len(output) != 2 or not re.fullmatch('[0-9a-f]{64}', output[0]):
            raise RuntimeError('Cannot identify the actual initializer binary')
        return output[0]

    def operator(self):
        return self.compose('run', '--rm', '--no-deps', '-T', '--entrypoint', 'id', 'itsm-init', '-un').stdout.decode().strip()

    def apply_preparation(self, evidence):
        self.write_json('preparation.json', evidence)
        self.migrate('-prepare-workitem', '-evidence-file', '/ga/preparation.json')

    def finish_bootstrap(self):
        # Canonical migrations own function ACLs; never reopen owner-only triggers.
        self.migrate('-up')
        result = self.init_command(seed=True)
        if result.returncode:
            raise RuntimeError('Post-P initializer failed')
        # Only the isolated owner provisions the existing standard-runtime
        # binding. Runtime admission still checks read-only registry privileges.
        self.sql("""GRANT SELECT ON public.execution_scopes,public.execution_scope_members,public.execution_runtime_bindings,public.execution_tool_invocations TO ga_app;
INSERT INTO public.execution_runtime_bindings(runtime_role,deployment_id,mode)
VALUES ('ga_app','%s','standard');
REVOKE ALL ON work_item_migration_evidence FROM ga_app;
REVOKE INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER ON schema_migrations FROM ga_app;""" % self.project)

    def provision_storage(self):
        # Reuse the owned service network and private runtime storage settings.
        self.compose('run', '--rm', '--no-deps', '-T', '--entrypoint', '/ga/provision-minio', 'itsm-backend')

    def verify_prepared(self):
        result = self.sql("SELECT count(*) FILTER (WHERE version='037_work_item_structure_preparation'),count(*) FILTER (WHERE version='038_work_item_controlled_retirement') FROM schema_migrations;")
        if result.strip() != b'1|0':
            raise RuntimeError('Expected one preparation receipt and no retirement')
        self.migrate('-status')


def assert_owned_config(config, project):
    if not re.fullmatch(r'ga-preparation-[a-z0-9-]+', project) or config.get('name') != project:
        raise RuntimeError('Invalid cleanup project identity')
    for kind in ('volumes', 'networks'):
        for name, resource in config.get(kind, {}).items():
            if resource.get('external') or resource.get('name') != project + '_' + name:
                raise RuntimeError('Cleanup refuses unowned resources')
    for name, service in config['services'].items():
        if service.get('container_name') != project + '-' + name:
            raise RuntimeError('Cleanup refuses an unowned container')


def redact_logs(config, raw):
    values = set()
    for service in config['services'].values():
        for key, value in service.get('environment', {}).items():
            if value and re.search(r'PASSWORD|SECRET|TOKEN|DSN|DATABASE_URL', key):
                values.add(str(value))
                if '://' in str(value):
                    password = urllib.parse.urlsplit(str(value)).password
                    if password:
                        values.add(urllib.parse.unquote(password))
    result = raw.decode(errors='replace')
    for value in sorted(values, key=len, reverse=True):
        result = result.replace(value, '[REDACTED]')
    return result


def verify_live_ownership(config, project):
    resources = [('container', service['container_name']) for service in config['services'].values()]
    for kind in ('volumes', 'networks'):
        resources.extend((kind[:-1], resource['name']) for resource in config.get(kind, {}).values())
    for kind, name in resources:
        result = subprocess.run(['docker', kind, 'inspect', name], capture_output=True)
        if result.returncode:
            if b'No such' in result.stderr:
                continue
            raise RuntimeError('Cannot verify disposable resource ownership')
        info = json.loads(result.stdout)[0]
        labels = info.get('Config', {}).get('Labels', {}) if kind == 'container' else info.get('Labels', {})
        if labels.get('com.docker.compose.project') != project or labels.get('itsm.ga.preparation') != project:
            raise RuntimeError('Cleanup refuses a resource without matching ownership labels')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('prepare', 'logs', 'cleanup'))
    args = parser.parse_args()
    if os.environ.get('GITHUB_ACTIONS') != 'true':
        parser.error('This provisioning fixture runs only in a disposable GitHub Actions job')
    if args.action == 'prepare':
        repo = Path(__file__).resolve().parents[1]
        project = 'ga-preparation-' + uuid.uuid4().hex[:16]
        folder = Path(tempfile.mkdtemp(prefix=project + '-'))
        folder.chmod(0o700)
        fixture = ComposeFixture(repo, folder, project)
        # Publish only private file location and random project name, never secrets.
        with open(os.environ['GITHUB_ENV'], 'a') as output:
            output.write('GA_PREPARATION_CONFIG=' + str(fixture.config) + '\nGA_PREPARATION_PROJECT=' + project + '\n')
        fixture.create()
        summary = prepare_database(fixture, project)
        fixture.provision_storage()
        # Compose dependencies still require the final initializer to succeed.
        fixture.compose('up', '-d', 'itsm-init', 'itsm-backend', 'itsm-frontend')
        (repo / 'ga-gate-preparation-summary.json').write_text(json.dumps(summary, indent=2) + '\n')
        print('Controlled P preparation completed; original readiness and smoke gates follow.')
        return
    filename = os.environ.get('GA_PREPARATION_CONFIG')
    if not filename or not Path(filename).is_file():
        return  # Preparation did not create any Compose resources.
    config = json.loads(Path(filename).read_text())
    project = os.environ.get('GA_PREPARATION_PROJECT', '')
    assert_owned_config(config, project)
    command = ['docker', 'compose', '--project-name', project, '-f', filename, '--profile', 'dev']
    if args.action == 'logs':
        result = subprocess.run([*command, 'logs', '--no-color', '--tail=200'], capture_output=True)
        if result.returncode:
            raise RuntimeError('Cannot collect disposable Compose logs')
        diagnostics = Path(filename).parent / 'private-command-errors.log'
        errors = diagnostics.read_bytes() if diagnostics.is_file() else b''
        print(redact_logs(config, result.stdout + result.stderr + errors))
    else:
        folder = Path(filename).resolve().parent
        if folder.parent != Path(tempfile.gettempdir()).resolve() or not folder.name.startswith(project + '-') or Path(filename).name != 'compose.json':
            raise RuntimeError('Cleanup refuses an unexpected private directory')
        verify_live_ownership(config, project)
        subprocess.run([*command, 'down', '-v'], check=True)
        # Includes private credentials, dump diagnostics and evidence; no uploads.
        shutil.rmtree(folder)


if __name__ == '__main__':
    try:
        main()
    except RuntimeError as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
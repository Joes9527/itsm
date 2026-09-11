#!/usr/bin/env python3
"""Run V1 against disposable Docker dependencies and exclusive app processes.

Requires Linux, Docker, Go, Node/npm, rsync, installed frontend node_modules and
Playwright Chromium. No shared service, credentials or database is reused.
Usage: python3 tests/e2e/fixtures/run-workitem-convergence.py --base-port 19490
Logs/traces stay in a private temporary directory printed at exit; environment
secrets and all owned containers/processes are removed even when tests fail.
"""
import argparse
import hashlib
from datetime import datetime, timezone
import json
import os
import pwd
import urllib.parse
from pathlib import Path
import secrets
import shutil
import signal
import socket
import subprocess
import tempfile
import time
import urllib.request
import uuid

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base-port', type=int, default=19490)
    parser.add_argument('--grep', default='.')
    args = parser.parse_args()
    def interrupted(_signum,_frame):
        raise KeyboardInterrupt('Isolated validation interrupted')
    signal.signal(signal.SIGTERM,interrupted)
    if not 1024 <= args.base_port <= 65530:
        parser.error('base-port must leave five unprivileged ports')
    repo = Path(__file__).resolve().parents[4]
    backend, frontend = repo/'itsm-backend', repo/'itsm-frontend'
    if not (frontend/'node_modules').is_dir():
        parser.error('Install frontend dependencies before this isolated runner')
    for program in ['docker','go','node','npx','rsync']:
        if not shutil.which(program):
            parser.error('Missing prerequisite: '+program)
    ports = list(range(args.base_port,args.base_port+5))
    for port in ports:
        with socket.socket() as probe:
            probe.bind(('127.0.0.1',port))  # Refuse occupied ports before any creation.
    run_id = 'workitem-v1-'+uuid.uuid4().hex[:12]
    folder = Path(tempfile.mkdtemp(prefix=run_id+'-'))
    os.chmod(folder,0o700)
    app_port, web_port, redis_port, minio_port, pg_port = ports
    pg, redis, minio = ['codex-'+run_id+'-'+name for name in ['pg','redis','minio']]
    containers, processes, secret_files = [], [], []
    password, app_password, system_password, minio_password, admin_password, inspection_password = [secrets.token_hex(24)+'aA!7' for _ in range(6)]
    log = (folder/'setup.log').open('w')
    source_commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip()
    def tree_hash(root, predicate):
        digest=hashlib.sha256()
        for path in sorted(root.rglob('*')):
            if path.is_file() and predicate(path):
                digest.update(str(path.relative_to(root)).encode()+b'\0'+path.read_bytes())
        return digest.hexdigest()
    tool_env_keys={'PATH','HOME','USER','LOGNAME','SHELL','LANG','LC_ALL','TZ','TMPDIR','TMP','TEMP','GOPATH','GOCACHE','GOMODCACHE','GOROOT','GOENV','CGO_ENABLED','CC','CXX','XDG_CACHE_HOME','PLAYWRIGHT_BROWSERS_PATH'}
    base_env={key:value for key,value in os.environ.items() if key in tool_env_keys}
    env = dict(base_env, DB_HOST='127.0.0.1',DB_USER='v1owner',DB_PASSWORD=password,DB_NAME='workitem_v1',DB_SCHEMA='public',DB_SSLMODE='disable',
               JWT_SECRET=secrets.token_hex(32),REDIS_HOST='127.0.0.1',REDIS_PORT=str(redis_port),REDIS_DB='0',
               MINIO_ENDPOINT='127.0.0.1:'+str(minio_port),MINIO_ROOT_USER='v1minio',MINIO_ROOT_PASSWORD=minio_password,MINIO_BUCKET='workitem-v1',
               FRONTEND_URL='http://127.0.0.1:'+str(web_port),ITSM_SEED_CONFIG=str(backend/'config/seed/default.json'),
               ENV='development',SERVER_MODE='release',DEPLOYMENT_MODE='private',RLS_MODE='off',
               ADMIN_USERNAME='admin',ADMIN_EMAIL='v1-admin@example.test',ADMIN_PASSWORD=admin_password,LOG_LEVEL='warn',LOG_PATH=str(folder/'logs'))
    def run(argv, **kwargs):
        return subprocess.run(argv,check=True,stdout=log,stderr=log,**kwargs)
    def wait_ready(check, seconds=180):
        deadline=time.monotonic()+seconds
        while time.monotonic()<deadline:
            try:
                if check():
                    return
            except (OSError,urllib.error.URLError):
                pass
            time.sleep(1)
        raise RuntimeError('Isolated service readiness timed out; see private setup log')
    def launch(argv,cwd,environment,name):
        handle=(folder/name).open('w')
        proc=subprocess.Popen(argv,cwd=cwd,env=environment,stdout=handle,stderr=subprocess.STDOUT,start_new_session=True)
        processes.append(proc)
        return proc
    def container(name,image,port,container_port,values,command=()):
        envfile=folder/(name+'.env')
        envfile.write_text('\n'.join(key+'='+value for key,value in values.items())+'\n')
        os.chmod(envfile,0o600);secret_files.append(envfile)
        containers.append(name)
        run(['docker','run','-d','--name',name,'--label','codex.workitem.v1='+run_id,
             '-p','127.0.0.1:'+str(port)+':'+str(container_port),'--env-file',str(envfile),image,*command])
    try:
        container(pg,'pgvector/pgvector:pg17',pg_port,5432,{'POSTGRES_DB':'workitem_v1','POSTGRES_USER':'v1owner','POSTGRES_PASSWORD':password})
        container(redis,'redis:7-alpine',redis_port,6379,{})
        container(minio,'minio/minio:latest',minio_port,9000,{'MINIO_ROOT_USER':'v1minio','MINIO_ROOT_PASSWORD':minio_password},['server','/data'])
        wait_ready(lambda: subprocess.run(['docker','exec',pg,'pg_isready','-U','v1owner','-d','workitem_v1'],stdout=log,stderr=log).returncode==0)
        config=(backend/'config.yaml').read_text().replace('port: 5432','port: '+str(pg_port),1).replace('port: 8090','port: '+str(app_port),1)
        config=config.replace('provider: openai        # openai, local','provider: local        # isolated validation')
        (folder/'config.yaml').write_text(config)
        run(['go','build','-o',str(folder/'backend'),'.'],cwd=backend,env=base_env)
        run(['go','build','-tags','migrate','-o',str(folder/'migrate'),'./cmd/migrate'],cwd=backend,env=base_env)
        control_file=folder/'migration-control.json'
        control={'DeploymentID':run_id,'InspectionRole':'v1inspect'}
        control_file.write_text(json.dumps(control));os.chmod(control_file,0o600);secret_files.append(control_file)
        env['ITSM_MIGRATION_CONTROL_FILE']=str(control_file)
        # A new empty schema stops at the same explicit P boundary as old profiles.
        initial=subprocess.run([str(folder/'backend')],cwd=folder,env=dict(env,ITSM_BOOTSTRAP_ONLY='true',ITSM_AUTO_MIGRATE='true',ITSM_AUTO_SEED='false'),stdout=log,stderr=log)
        log.flush()
        if initial.returncode==0 or 'runtime requires migration 037_work_item_structure_preparation' not in (folder/'setup.log').read_text():
            raise RuntimeError('Empty bootstrap did not stop at the controlled preparation boundary')
        sql = """
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE CREATE ON DATABASE workitem_v1 FROM PUBLIC;
REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA public FROM PUBLIC;
CREATE ROLE v1app LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD '%s';
GRANT CONNECT ON DATABASE workitem_v1 TO v1app;
GRANT USAGE ON SCHEMA public TO v1app;
GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO v1app;
GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO v1app;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO v1app;
CREATE ROLE v1system LOGIN NOSUPERUSER BYPASSRLS NOCREATEROLE NOCREATEDB NOREPLICATION NOINHERIT PASSWORD '%s';
GRANT CONNECT ON DATABASE workitem_v1 TO v1system;
GRANT USAGE ON SCHEMA public TO v1system;
GRANT SELECT ON users,tenants,msp_allocations,process_callback_outboxes,external_identities,connector_configs TO v1system;
GRANT SELECT,UPDATE ON outbox_events,ticket_notifications TO v1system;
GRANT INSERT ON audit_logs TO v1system;
GRANT SELECT(id) ON audit_logs TO v1system;
GRANT USAGE ON SEQUENCE audit_logs_id_seq TO v1system;
""" % (app_password,system_password)
        # stdin carries generated passwords; never put them in commands or logs.
        result=subprocess.run(['docker','exec','-i',pg,'psql','-U','v1owner','-d','workitem_v1','-v','ON_ERROR_STOP=1','-q'],input=sql,text=True,capture_output=True)
        if result.returncode:
            raise RuntimeError('Dedicated database role initialization failed (details withheld to protect credentials)')
        inspector_sql="CREATE ROLE v1inspect LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB NOREPLICATION NOINHERIT PASSWORD '%s';" % inspection_password
        result=subprocess.run(['docker','exec','-i',pg,'psql','-U','v1owner','-d','workitem_v1','-v','ON_ERROR_STOP=1','-q'],input=inspector_sql,text=True,capture_output=True)
        if result.returncode:
            raise RuntimeError('Dedicated inspection role initialization failed')
        control['ReviewedGrants']=[{'Role':'v1app','Table':table,'Privileges':['SELECT','INSERT','UPDATE','DELETE']} for table in ['tickets','incidents','problems','changes']]
        control_file.write_text(json.dumps(control))
        inventory_run=subprocess.run([str(folder/'migrate'),'-prepare-workitem','-dry-run'],cwd=folder,env=env,stdout=subprocess.PIPE,stderr=log,text=True,check=True)
        inventory=json.loads(inventory_run.stdout)
        # Actual isolated pre-P backup and restore; this is not Task6 post-R recovery proof.
        backup=subprocess.check_output(['docker','exec',pg,'pg_dump','-U','v1owner','-d','workitem_v1','-Fc'],stderr=log)
        run(['docker','exec',pg,'createdb','-U','v1owner','workitem_v1_restore_p'])
        restored=subprocess.run(['docker','exec','-i',pg,'pg_restore','-U','v1owner','-d','workitem_v1_restore_p','--exit-on-error'],input=backup,stdout=log,stderr=log)
        if restored.returncode:
            raise RuntimeError('Isolated pre-P backup restore failed')
        restored_ledger=subprocess.check_output(['docker','exec',pg,'psql','-U','v1owner','-d','workitem_v1_restore_p','-Atc','SELECT version,checksum FROM schema_migrations ORDER BY version'],stderr=log)
        source_ledger=subprocess.check_output(['docker','exec',pg,'psql','-U','v1owner','-d','workitem_v1','-Atc','SELECT version,checksum FROM schema_migrations ORDER BY version'],stderr=log)
        if restored_ledger!=source_ledger:
            raise RuntimeError('Isolated pre-P restore ledger differs')
        evidence=dict(inventory,CatalogRevision='workitem-controlled-retirement-v1',
                      ApplicationDigest=hashlib.sha256((folder/'backend').read_bytes()).hexdigest(),
                      BackupDigest=hashlib.sha256(backup).hexdigest(),RestoreReportDigest=hashlib.sha256(restored_ledger).hexdigest(),
                      Operator=pwd.getpwuid(os.getuid()).pw_name,ChangeRecord=run_id)
        evidence_file=folder/'preparation-evidence.json';evidence_file.write_text(json.dumps(evidence));os.chmod(evidence_file,0o600)
        run([str(folder/'migrate'),'-prepare-workitem','-evidence-file',str(evidence_file)],cwd=folder,env=env)
        run([str(folder/'migrate'),'-up'],cwd=folder,env=env)
        # Existing targets now use only the migration stream; Ent is not overlaid.
        run([str(folder/'backend')],cwd=folder,env=dict(env,ITSM_BOOTSTRAP_ONLY='true',ITSM_AUTO_MIGRATE='true',ITSM_AUTO_SEED='true'))
        grant_new="GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO v1app; GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO v1app; GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO v1app; REVOKE ALL ON work_item_migration_evidence FROM v1app; REVOKE INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER ON schema_migrations FROM v1app;"
        result=subprocess.run(['docker','exec','-i',pg,'psql','-U','v1owner','-d','workitem_v1','-v','ON_ERROR_STOP=1','-q'],input=grant_new,text=True,capture_output=True)
        if result.returncode:
            raise RuntimeError('Dedicated post-migration business grants failed')
        env['ITSM_MIGRATION_INSPECTION_DSN']='postgresql://v1inspect:'+urllib.parse.quote(inspection_password,safe='')+'@127.0.0.1:'+str(pg_port)+'/workitem_v1?sslmode=disable&search_path=public'
        env.update(DB_USER='v1app',DB_PASSWORD=app_password,DB_SYSTEM_ROLE_USER='v1system',DB_SYSTEM_ROLE_PASSWORD=system_password,RLS_MODE='enforce',ITSM_AUTO_MIGRATE='false',ITSM_AUTO_SEED='false')
        launch([str(folder/'backend')],folder,env,'backend.log')
        def backend_ready():
            try:
                urllib.request.urlopen('http://127.0.0.1:'+str(app_port)+'/api/v1/auth/me',timeout=2)
            except urllib.error.HTTPError as error:
                return error.code==401
            return True
        wait_ready(backend_ready)
        run(['rsync','-a','--exclude=node_modules','--exclude=.next','--exclude=.env*','--exclude=playwright-report','--exclude=test-results',str(frontend)+'/',str(folder/'frontend')+'/'])
        (folder/'frontend/node_modules').symlink_to(frontend/'node_modules',target_is_directory=True)
        front_env=dict(base_env,ITSM_BACKEND_URL='http://127.0.0.1:'+str(app_port),NEXT_PUBLIC_API_URL='')
        launch(['node',str(frontend/'node_modules/next/dist/bin/next'),'dev','--hostname','127.0.0.1','--port',str(web_port)],folder/'frontend',front_env,'frontend.log')
        wait_ready(lambda: urllib.request.urlopen('http://127.0.0.1:'+str(web_port)+'/login',timeout=30).status==200)
        manifest={'runId':run_id,'baseURL':'http://127.0.0.1:'+str(web_port),'apiURL':'http://127.0.0.1:'+str(app_port),'postgresContainer':pg,'containers':containers,'ports':ports}
        (folder/'resources.json').write_text(json.dumps(manifest,indent=2))
        manifest.update(sourceCommit=source_commit,backendBuiltAtUTC=datetime.fromtimestamp((folder/'backend').stat().st_mtime,timezone.utc).isoformat(),backendBinarySHA256=hashlib.sha256((folder/'backend').read_bytes()).hexdigest(),
                        backendProductionGoSHA256=tree_hash(backend,lambda p:p.suffix=='.go' and not p.name.endswith('_test.go')),
                        frontendCopiedSourceSHA256=tree_hash(folder/'frontend/src',lambda p:True))
        (folder/'resources.json').write_text(json.dumps(manifest,indent=2))
        test_env=dict(base_env,PLAYWRIGHT_EXTERNAL_SERVER='1',PLAYWRIGHT_BASE_URL=manifest['baseURL'],NEXT_PUBLIC_API_URL=manifest['apiURL'],
                      PLAYWRIGHT_V1_ISOLATED=run_id,PLAYWRIGHT_V1_RESOURCE_MANIFEST=str(folder/'resources.json'),PLAYWRIGHT_V1_ADMIN_PASSWORD=admin_password,
                      PLAYWRIGHT_OUTPUT_DIR=str(folder/'test-results'),PLAYWRIGHT_HTML_OUTPUT_DIR=str(folder/'report'))
        with (folder/'tests.log').open('w') as test_log:
            test_process=subprocess.Popen(['npx','playwright','test','tests/e2e/business-flows/workitem-convergence.spec.ts','--project=business-flows','--workers=1','--grep',args.grep],cwd=frontend,env=test_env,stdout=test_log,stderr=subprocess.STDOUT,start_new_session=True)
            processes.append(test_process)
            test_process.wait()
            result=test_process
        print('\n'.join((folder/'tests.log').read_text().splitlines()[-20:]))
        return result.returncode
    finally:
        cleanup_errors=[]
        for proc in reversed(processes):
            try:
                if proc.poll() is None:
                    os.killpg(proc.pid,signal.SIGTERM)
                    try:
                        proc.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        os.killpg(proc.pid,signal.SIGKILL);proc.wait()
            except ProcessLookupError:
                pass
            except Exception as error:
                cleanup_errors.append('process '+str(proc.pid)+': '+type(error).__name__)
        for name in reversed(containers):
            try:
                info=subprocess.run(['docker','inspect',name],text=True,capture_output=True)
                if info.returncode==0:
                    actual=json.loads(info.stdout)[0]
                    if actual['Config'].get('Labels',{}).get('codex.workitem.v1')!=run_id:
                        cleanup_errors.append('container ownership mismatch: '+name)
                        continue
                    removed=subprocess.run(['docker','rm','-f',name],stdout=log,stderr=log)
                    verified=subprocess.run(['docker','inspect',name],text=True,capture_output=True)
                    absent=verified.returncode!=0 and ('no such object:' in verified.stderr.lower() or 'no such container:' in verified.stderr.lower())
                    if removed.returncode or not absent:
                        cleanup_errors.append('container removal not verified: '+name)
                elif 'no such object:' not in info.stderr.lower() and 'no such container:' not in info.stderr.lower():
                    cleanup_errors.append('container inspection failed: '+name)
            except Exception as error:
                cleanup_errors.append('container '+name+': '+type(error).__name__)
        for path in secret_files:
            try:
                path.unlink(missing_ok=True)
            except OSError:
                cleanup_errors.append('private environment file: '+path.name)
        # Traces contain generated test credentials: preserve only in this 0700 directory.
        shutil.rmtree(folder/'frontend',ignore_errors=True)
        (folder/'backend').unlink(missing_ok=True)
        print('Private evidence: '+str(folder))
        if cleanup_errors:
            print('Cleanup requires attention: '+ '; '.join(cleanup_errors))
        else:
            print('Owned containers/processes removed; generated environment files removed.')
        log.close()
        if cleanup_errors:
            raise RuntimeError('Incomplete isolated resource cleanup')

if __name__=='__main__':
    raise SystemExit(main())

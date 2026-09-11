"""Full physical recovery and actual controlled R for the disposable V1 fixture.

This is test orchestration, not a migration executor. Only the compiled production
CLI executes P/R. Physical copies preserve immutable receipt and role/OID identity.
"""
import base64
import copy
import hashlib
import hmac
import json
import os
import signal
import subprocess
import time
import tarfile
import io
import urllib.parse
import urllib.request
from datetime import datetime, timezone


def sha(value):
    return hashlib.sha256(value).hexdigest()


def encode(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def now():
    return datetime.now(timezone.utc).isoformat().replace('+00:00', 'Z')


def verify_recovery(expected, actual, uncaptured=()):
    if uncaptured:
        raise ValueError('Uncaptured post-backup data prevents a zero-loss verdict')
    required={'tables','sequences','roles_acl','attachments','application','consumers','configuration','control','config_yaml'}
    if not required.issubset(expected) or not required.issubset(actual):
        raise ValueError('Complete recovery manifest required')
    for surface in set(expected) | set(actual):
        if surface not in expected or surface not in actual or expected[surface] != actual[surface]:
            raise ValueError('Recovery mismatch: '+surface)
    return True


class Recovery:
    def __init__(self, context):
        self.c = context
        self.folder = context['folder']
        self.journal = {'scope': 'isolated test authority only; no target-environment approval', 'events': []}
        self.negative = []

    def save(self, name, value):
        (self.folder/name).write_bytes(encode(value))

    def event(self, action, **facts):
        self.journal['events'].append(dict(at=now(), action=action, **facts))
        self.save('recovery-journal.json', self.journal)

    def command(self, argv, data=None):
        p = subprocess.run(argv, input=data, stdout=subprocess.PIPE, stderr=self.c['log'], check=True)
        return p.stdout

    def sql(self, pg, sql):
        return self.command(['docker','exec','-i',pg,'psql','-U','v1owner','-d','workitem_v1','-At','-v','ON_ERROR_STOP=1'], sql.encode()).decode().strip()

    def s3_get(self, endpoint, key, method="GET", data=None):
        # SigV4 uses only generated isolated credentials; no host SDK credential chain.
        uri='/workitem-v1/'+urllib.parse.quote(key, safe='/~')
        stamp=datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ'); day=stamp[:8]
        empty=sha(data or b''); headers='host:'+endpoint+'\nx-amz-content-sha256:'+empty+'\nx-amz-date:'+stamp+'\n'
        signed='host;x-amz-content-sha256;x-amz-date'
        request=method+'\n'+uri+'\n\n'+headers+'\n'+signed+'\n'+empty
        scope=day+'/us-east-1/s3/aws4_request'
        tosign='AWS4-HMAC-SHA256\n'+stamp+'\n'+scope+'\n'+sha(request.encode())
        keybytes=('AWS4'+self.c['minio_password']).encode()
        for part in [day,'us-east-1','s3','aws4_request']:
            keybytes=hmac.new(keybytes,part.encode(),hashlib.sha256).digest()
        signature=hmac.new(keybytes,tosign.encode(),hashlib.sha256).hexdigest()
        auth='AWS4-HMAC-SHA256 Credential=v1minio/'+scope+', SignedHeaders='+signed+', Signature='+signature
        req=urllib.request.Request('http://'+endpoint+uri,data=data,method=method,headers={'Authorization':auth,'x-amz-content-sha256':empty,'x-amz-date':stamp})
        return urllib.request.urlopen(req,timeout=20).read()

    def snapshot(self, pg, endpoint, environment, bundle=None):
        if bundle:
            environment=json.loads((bundle/"runtime-environment.json").read_bytes())
        tables={}
        names=self.sql(pg,"SELECT quote_ident(schemaname)||'.'||quote_ident(tablename) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY 1").splitlines()
        queries=["SELECT '"+name+"' AS name,coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]') AS content FROM "+name+" t" for name in names]
        contents=json.loads(self.sql(pg,"SELECT jsonb_object_agg(name,content)::text FROM ("+' UNION ALL '.join(queries)+") all_tables"))
        for name,content in contents.items():
            tables[name]={'rows':len(content),'sha256':sha(encode(content))}
        sequences={}
        sequence_names=self.sql(pg,"SELECT quote_ident(schemaname)||'.'||quote_ident(sequencename) FROM pg_sequences ORDER BY 1").splitlines()
        queries=["SELECT '"+name+"' AS name,json_build_array(last_value,is_called)::text AS content FROM "+name for name in sequence_names]
        if queries:
            sequences=json.loads(self.sql(pg,"SELECT json_object_agg(name,content)::text FROM ("+' UNION ALL '.join(queries)+") all_sequences"))
        schema=self.command(['docker','exec',pg,'pg_dump','-U','v1owner','-d','workitem_v1','--schema-only']).decode()
        schema='\n'.join(line for line in schema.splitlines() if not line.startswith(('\\restrict','\\unrestrict')))
        roles=self.sql(pg,"SELECT jsonb_agg(to_jsonb(r) ORDER BY rolname)::text FROM pg_authid r")
        attachments={}
        rows=json.loads(self.sql(pg,"SELECT coalesce(json_agg(row_to_json(a)),'[]') FROM (SELECT id,file_path,file_size FROM ticket_attachments ORDER BY id) a"))
        for row in rows:
            data=self.s3_get(endpoint,row['file_path'])
            if len(data)!=row['file_size']:
                raise ValueError('Attachment bytes differ from database size')
            attachments[str(row['id'])]={'path':row['file_path'],'bytes':len(data),'sha256':sha(data)}
        # A live MinIO process does not establish actual backend storage selection.
        for uploads in [self.folder/'uploads']:
            if uploads.exists() and any(p.is_file() for p in uploads.rglob('*')):
                raise ValueError('Unexpected local attachment fallback must be included explicitly')
        # Transport addresses change for independent recovery; behavior and secrets do not.
        excluded={'DB_HOST','MINIO_ENDPOINT','ITSM_MIGRATION_INSPECTION_DSN'}
        cfg={k:v for k,v in environment.items() if k not in excluded}
        return {'tables':tables,'sequences':sequences,'roles_acl':sha((roles+schema).encode()),'attachments':attachments,
                'application':sha(((bundle or self.folder)/'backend').read_bytes()),
                'consumers':sha(encode({k:v for k,v in cfg.items() if k.startswith(('REDIS','ITSM_AUTO','RLS','DB_SYSTEM','ENV','DEPLOYMENT'))})),
                'configuration':sha(encode(cfg)), 'control':sha(((bundle/'migration-control.json') if bundle else self.c['control_file']).read_bytes()),
                'config_yaml':sha(((bundle or self.folder)/'config.yaml').read_bytes())}

    def archive(self, pg, minio, label):
        # pg_basebackup includes every DB, role, ACL, sequence, WAL and immutable bytea receipt.
        remote='/tmp/task6-'+label
        self.command(['docker','exec',pg,'pg_basebackup','-U','v1owner','-D',remote,'-Fp','-X','stream','-c','fast'])
        self.command(['docker','exec',pg,'pg_verifybackup',remote])
        backup_manifest=json.loads(self.command(['docker','exec',pg,'cat',remote+'/backup_manifest']))
        self.event('physical-backup-consistency',verified=True,walRanges=backup_manifest.get('WAL-Ranges'),systemIdentifier=backup_manifest.get('System-Identifier'),postgresVersion=self.sql(pg,'SHOW server_version'))
        archives={}
        for kind, container, directory in [('postgres',pg,remote),('objects',minio,'/data')]:
            destination=self.folder/(label+'-'+kind+'.tar')
            if kind=='objects': self.command(['docker','stop',container])
            with destination.open('wb') as out:
                command=['docker','cp',container+':/data/.','-'] if kind=='objects' else ['docker','exec',container,'tar','-C',directory,'-cf','-','.']
                subprocess.run(command,stdout=out,stderr=self.c['log'],check=True)
            if kind=='objects':
                self.command(['docker','start',container])
                self.c['wait_ready'](lambda: urllib.request.urlopen('http://'+self.c['env']['MINIO_ENDPOINT']+'/minio/health/live',timeout=2).status==200)
            os.chmod(destination,0o600);self.c['secret_files'].append(destination)
            archives[kind]={'file':str(destination),'sha256':sha(destination.read_bytes()),'bytes':destination.stat().st_size}
        config_archive=self.folder/(label+'-configuration.tar')
        with tarfile.open(config_archive,'w') as tar:
            for name in ['backend','migrate','config.yaml','migration-control.json']:
                tar.add(self.folder/name,arcname=name)
            content=encode(self.c['env']);info=tarfile.TarInfo('runtime-environment.json');info.size=len(content);info.mode=0o600
            tar.addfile(info,io.BytesIO(content))
        os.chmod(config_archive,0o600);self.c['secret_files'].append(config_archive)
        archives['configuration']={'file':str(config_archive),'sha256':sha(config_archive.read_bytes()),'bytes':config_archive.stat().st_size}
        self.event('full-backup', label=label, artifacts=archives)
        return archives

    def restore_container(self, name, image, port, container_port, archive, directory, command):
        self.c['containers'].append(name)
        values=['--env-file',str(self.folder/('codex-'+self.c['run_id']+'-minio.env'))] if container_port==9000 else []
        self.command(['docker','create',*values,'--name',name,'--label','codex.workitem.v1='+self.c['run_id'],'-p','127.0.0.1:'+str(port)+':'+str(container_port),image,*command])
        with open(archive,'rb') as inp:
            subprocess.run(['docker','cp','-',name+':'+directory],stdin=inp,stdout=self.c['log'],stderr=self.c['log'],check=True)
        self.command(['docker','start',name])
        info=json.loads(self.command(['docker','inspect',name]))[0]
        self.c['owned_ids'][name]=info['Id']
        self.event('owned-resource',name=name,containerID=info['Id'],imageID=info['Image'],image=info['Config']['Image'])
        if info['Config']['Labels'].get('codex.workitem.v1')!=self.c['run_id']:
            raise ValueError('Recovery resource ownership mismatch')
        return info['Id']

    def restore(self, archives, suffix, offset):
        pg='codex-'+self.c['run_id']+'-'+suffix+'-pg';minio='codex-'+self.c['run_id']+'-'+suffix+'-minio'
        port=self.c['args'].base_port+offset
        pgid=self.restore_container(pg,'pgvector/pgvector:pg17',port,5432,archives['postgres']['file'],'/var/lib/postgresql/data',['postgres'])
        mid=self.restore_container(minio,'minio/minio:latest',port+1,9000,archives['objects']['file'],'/data',['server','/data'])
        self.c['wait_ready'](lambda: subprocess.run(['docker','exec',pg,'pg_isready','-U','v1owner','-d','workitem_v1'],stdout=self.c['log'],stderr=self.c['log']).returncode==0)
        # Restored MinIO credentials are persisted in its data, and env credentials are also explicit.
        self.c['wait_ready'](lambda: urllib.request.urlopen('http://127.0.0.1:'+str(port+1)+'/minio/health/live',timeout=2).status==200)
        bundle=self.folder/(suffix+'-configuration');bundle.mkdir(mode=0o700)
        with tarfile.open(archives['configuration']['file']) as tar:
            tar.extractall(bundle,filter='data')
        self.bundles=getattr(self,'bundles',{});self.bundles[pg]=bundle
        for path in bundle.iterdir(): self.c['secret_files'].append(path)
        self.event('independent-restore', target=pg, containerID=pgid, objectContainerID=mid, logicalDatabase='workitem_v1', logicalDeployment=self.c['run_id'])
        return pg, minio, port

    def pause(self):
        proc=self.c['backend_process']
        if proc.poll() is None:
            os.killpg(proc.pid,signal.SIGTERM);proc.wait(timeout=20)
        self.event('application-and-inprocess-consumers-stopped', pid=proc.pid)

    def negative_checks(self, expected):
        for surface in ['tables','sequences','roles_acl','attachments','application','consumers','configuration','control','config_yaml']:
            candidate=copy.deepcopy(expected)
            candidate[surface]={'missing':'actual recovery verifier must reject'}
            try: verify_recovery(expected,candidate)
            except ValueError: self.negative.append(surface)
            else: raise AssertionError('Recovery falsely passed '+surface)
        for table in ['public.process_instances','public.audit_logs','public.schema_migrations','public.work_item_migration_evidence']:
            if table not in expected['tables']: raise ValueError('Required recovery surface missing '+table)
            candidate=copy.deepcopy(expected);del candidate['tables'][table]
            try: verify_recovery(expected,candidate)
            except ValueError: self.negative.append(table)
            else: raise AssertionError('Recovery falsely passed missing '+table)
        try: verify_recovery(expected,expected,uncaptured=['post-backup-write'])
        except ValueError: self.negative.append('uncaptured-post-backup-write')
        else: raise AssertionError('Uncaptured write accepted')

    def actual_fault_checks(self, expected, pg, endpoint, env):
        bundle=self.bundles[pg]
        def reject(name):
            try:
                candidate=self.snapshot(pg,endpoint,env,bundle=bundle)
                verify_recovery(expected,candidate)
            except (ValueError, urllib.error.HTTPError):
                self.negative.append('actual-'+name)
                self.event('actual-recovery-fault-rejected',fault=name,target=pg)
            else: raise AssertionError('Actual recovery fault accepted '+name)
        # Actual missing workflow/audit/receipt rows, preserved byte-for-byte after each fault.
        for table in ['audit_logs','process_instances','schema_migrations','work_item_migration_evidence']:
            identifier='version' if table in ['schema_migrations','work_item_migration_evidence'] else 'id'
            rows=json.loads(self.sql(pg,"SELECT coalesce(json_agg(row_to_json(t)),'[]') FROM (SELECT * FROM "+table+" LIMIT 1) t"))
            if not rows: raise ValueError('Required actual fault row missing '+table)
            row=rows[0];literal="'"+str(row[identifier]).replace("'","''")+"'"
            if table=='process_instances':
                # A parent row cannot be removed while real tasks reference it. Lose its
                # actual persisted identity content without disabling those constraints.
                self.sql(pg,"UPDATE process_instances SET business_key='' WHERE id="+literal)
                try: reject('missing-process-identity-content')
                finally: self.sql(pg,"UPDATE process_instances SET business_key='"+row['business_key'].replace("'","''")+"' WHERE id="+literal)
            else:
                self.sql(pg,'DELETE FROM '+table+' WHERE '+identifier+'='+literal)
                try: reject('missing-'+table)
                finally:
                    raw=json.dumps(row).replace("'","''")
                    self.sql(pg,"INSERT INTO "+table+" SELECT * FROM json_populate_record(NULL::"+table+",'"+raw+"'::json)")
        attachment=next(iter(expected['attachments'].values()));key=attachment['path'];data=self.s3_get(endpoint,key)
        self.s3_get(endpoint,key,method='DELETE')
        try: reject('missing-attachment-object')
        finally: self.s3_get(endpoint,key,method='PUT',data=data)
        for filename in ['backend','runtime-environment.json','config.yaml']:
            file=bundle/filename;original=file.read_bytes()
            if filename=='runtime-environment.json':
                value=json.loads(original);value['ITSM_AUTO_SEED']='true';file.write_bytes(encode(value))
            else: file.write_bytes(original+b'\nwrong-recovered-version\n')
            try: reject('wrong-'+filename)
            finally: file.write_bytes(original)
        verify_recovery(expected,self.snapshot(pg,endpoint,env,bundle=bundle))
        self.event('actual-fault-target-restored-and-reverified',target=pg)

    def retire(self, baseline, archives, restored, journey, observation):
        ownerenv=dict(self.c['env'],DB_USER='v1owner',DB_PASSWORD=self.c['password'])
        inv=json.loads(subprocess.check_output([str(self.folder/'migrate'),'-retire-workitem','-dry-run'],cwd=self.folder,env=ownerenv,stderr=self.c['log']))
        if not inv['Objects']: raise ValueError('Actual retained legacy structures required')
        r=dict(PreparationDigest=inv['PreparationDigest'],DataDigest=inv['DataDigest'],Objects=inv['Objects'],EmptyInventory=False,
               ObservationStartedAt=self.started,ObservationEndedAt=self.ended,PausedAt=self.paused,FinalRestorePointAt=self.finalpoint,Reports=[])
        e=dict(Target=inv['Target'],CatalogRevision='workitem-controlled-retirement-v1',LedgerDigest=inv['LedgerDigest'],InventoryDigest=inv['InventoryDigest'],
               ApplicationDigest=baseline['application'],Operator=self.c['evidence']['Operator'],ChangeRecord=self.c['run_id'],Retirement=r)
        for kind,body,field in [('backup',archives,'BackupDigest'),('restore',restored,'RestoreReportDigest'),('journey',journey,'JourneyReportDigest'),('observation',observation,'ObservationReportDigest')]:
            content=encode(body);digest=sha(content);e[field]=digest
            r['Reports'].append(dict(Result='passed',Kind=kind,Target=e['Target'],ApplicationDigest=e['ApplicationDigest'],LedgerDigest=e['LedgerDigest'],InventoryDigest=e['InventoryDigest'],
                                    PreparationDigest=r['PreparationDigest'],DataDigest=r['DataDigest'],RecordedAt=now(),Content=base64.b64encode(content).decode(),Digest=digest))
        signed=self.command([str(self.folder/'sign-fixture')],encode({'Evidence':e,'Private':self.c['key']['private']}))
        file=self.folder/'retirement-evidence.json';file.write_bytes(signed);os.chmod(file,0o600)
        self.c['run']([str(self.folder/'migrate'),'-retire-workitem','-evidence-file',str(file)],cwd=self.folder,env=ownerenv)
        self.event('actual-compiled-R-committed', evidenceSHA256=sha(signed), inventory=inv, receiptCount=self.sql(self.c['pg'],"SELECT count(*) FROM schema_migrations WHERE version='038_work_item_controlled_retirement'"))
        # Never rewrite the pre-R backup to add a later receipt.
        if self.sql(self.c['pg'],"SELECT to_regclass('public.workflows') IS NULL")!='t': raise ValueError('R did not retire actual legacy workflow structure')

"""Focused preflight for Redis recovery; only explicitly owned local containers."""
import copy
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import unittest
import uuid
from workitem_recovery_redis import redis_call,redis_snapshot,compare_redis,validate_security_transition,digest

class SecurityPolicyTests(unittest.TestCase):
    def test_missing_wrong_or_broadened_policy_rejects(self):
        old={'JWT_SECRET':'a'*64,'REDIS_PORT':'1','ITSM_AUTO_SEED':'false'}
        new=dict(old,JWT_SECRET='b'*64,REDIS_PORT='2')
        policy={'strategy':'restore-all-redis-and-rotate-existing-JWT-secret','changedField':'JWT_SECRET','oldDigest':digest(old['JWT_SECRET'].encode()),'newDigest':digest(new['JWT_SECRET'].encode())}
        validate_security_transition(old,new,policy)
        for bad in [None,dict(policy,newDigest='wrong'),dict(policy,changedField='all')]:
            with self.assertRaises(ValueError):validate_security_transition(old,new,bad)
        with self.assertRaises(ValueError):validate_security_transition(old,dict(new,ITSM_AUTO_SEED='true'),policy)
    def test_missing_security_family_evidence_rejects(self):
        missing={'observedAtMS':0,'databases':16,'keys':{},'families':{}}
        with self.assertRaises(ValueError):compare_redis(missing,copy.deepcopy(missing))

class ActualRedisRecoveryTests(unittest.TestCase):
    def test_pending_ttl_and_original_unavailable(self):
        self.assertEqual(os.getenv('WORKITEM_REDIS_RECOVERY_TEST'),'1','Explicit owned fixture opt-in required')
        for port in [19750,19751]:
            with socket.socket() as probe:
                probe.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
                probe.bind(('127.0.0.1',port))
        run='task6-redis-'+uuid.uuid4().hex[:12];owned=[]
        def cmd(args,**kw):return subprocess.check_output(args,stderr=subprocess.PIPE,**kw)
        image=cmd(['docker','image','inspect','redis:7-alpine','--format','{{.Id}}']).decode().strip()
        def create(suffix,port):
            name='codex-'+run+'-'+suffix
            cid=cmd(['docker','create','--name',name,'--label','codex.task6.redis='+run,'-p','127.0.0.1:'+str(port)+':6379',image]).decode().strip()
            info=json.loads(cmd(['docker','inspect',cid]))[0]
            owned.append((cid,[m['Name'] for m in info['Mounts'] if m['Type']=='volume']))
            self.assertEqual(info['Image'],image);return cid
        def start(cid,port):
            cmd(['docker','start',cid])
            for _ in range(60):
                try:
                    if redis_call(port,'PING')==b'PONG':return
                except OSError:pass
                time.sleep(.1)
            self.fail('Owned Redis unavailable')
        try:
            source=create('source',19750);start(source,19750)
            ids=[redis_call(19750,'XADD','task6.pending','*','message',v) for v in ['acked','pending','queued']]
            redis_call(19750,'XGROUP','CREATE','task6.pending','group','0')
            redis_call(19750,'XREADGROUP','GROUP','group','consumer','COUNT',2,'STREAMS','task6.pending','>')
            redis_call(19750,'XACK','task6.pending','group',ids[0])
            redis_call(19750,'SET','jwt:refresh:consumed:test','1','PX',60000)
            redis_call(19750,'SET','task6:expiry','value','PX',1500)
            expected=redis_snapshot(19750)
            redis_call(19750,'SAVE');cmd(['docker','exec',source,'redis-check-rdb','/data/dump.rdb']);cmd(['docker','stop',source])
            backup=cmd(['docker','cp',source+':/data/.','-'])
            restored=create('restored',19751)
            subprocess.run(['docker','cp','-',restored+':/data'],input=backup,check=True,capture_output=True)
            start(restored,19751);compare_redis(expected,redis_snapshot(19751))
            self.assertEqual(redis_call(19751,'XPENDING','task6.pending','group')[0],1)
            raw=redis_call(19751,'DUMP','task6.pending');redis_call(19751,'XDEL','task6.pending',ids[1])
            with self.assertRaises(ValueError):compare_redis(expected,redis_snapshot(19751))
            redis_call(19751,'RESTORE','task6.pending',0,raw,'REPLACE')
            with self.assertRaises(OSError):redis_snapshot(19750)
            time.sleep(1.6);compare_redis(expected,redis_snapshot(19751))
            self.assertIsNone(redis_call(19751,'GET','task6:expiry'))
            self.assertEqual(redis_call(19751,'GET','jwt:refresh:consumed:test'),b'1')
        finally:
            for cid,volumes in reversed(owned):
                cmd(['docker','rm','-f','-v',cid])
                self.assertNotEqual(subprocess.run(['docker','inspect',cid],capture_output=True).returncode,0)
                for volume in volumes:self.assertNotEqual(subprocess.run(['docker','volume','inspect',volume],capture_output=True).returncode,0)

if __name__=='__main__':unittest.main()

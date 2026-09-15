"""Redis and auth recovery fixture: full state, absolute TTL, explicit JWT epoch.
No production cache reset or auth policy is implemented here.
"""
import base64
import copy
import hashlib
import http.cookiejar
import json
import secrets
import socket
import time
import urllib.error
import urllib.request


def digest(value): return hashlib.sha256(value).hexdigest()

def redis_call(port, *args):
    encoded=[a if isinstance(a,bytes) else str(a).encode() for a in args]
    wire=b'*'+str(len(encoded)).encode()+b'\r\n'+b''.join(b'$'+str(len(a)).encode()+b'\r\n'+a+b'\r\n' for a in encoded)
    with socket.create_connection(('127.0.0.1',int(port)),timeout=5) as sock:
        sock.sendall(wire);reader=sock.makefile('rb')
        def read():
            line=reader.readline();kind=line[:1];value=line[1:-2]
            if kind==b'+': return value
            if kind==b'-': raise ValueError('Redis command rejected: '+value.decode())
            if kind==b':': return int(value)
            if kind==b'$':
                size=int(value)
                if size<0:return None
                result=reader.read(size);assert reader.read(2)==b'\r\n';return result
            if kind==b'*':
                size=int(value);return None if size<0 else [read() for _ in range(size)]
            raise ValueError('Invalid Redis reply')
        try: return read()
        finally: reader.close()


def redis_snapshot(port):
    if redis_call(port,'PING')!=b'PONG': raise ValueError('Healthy independent Redis required')
    dbs=int(redis_call(port,'CONFIG','GET','databases')[1]);result={'observedAtMS':int(time.time()*1000),'databases':dbs,'keys':{}}
    # Fixture uses DB0. Reject unexpected other logical DB data rather than omit it.
    keyspace=redis_call(port,'INFO','keyspace').decode()
    if any(line.startswith('db') and not line.startswith('db0:') for line in keyspace.splitlines()):
        raise ValueError('Unexpected Redis database requires explicit recovery inventory')
    families={'jwt:refresh:consumed:':0,'jwt:revoked:':0,'intake:identity-exchange:nonce:':0,'stream':0,'other-preserved':0}
    cursor=0
    while True:
        cursor,keys=redis_call(port,'SCAN',cursor,'COUNT',1000)
        for key in keys:
            value=redis_call(port,'DUMP',key);expiry=redis_call(port,'PEXPIRETIME',key)
            if value is None or expiry==-2: continue
            kind=redis_call(port,'TYPE',key).decode()
            family=next((p for p in families if key.startswith(p.encode()) and p.endswith(':')), 'stream' if kind=='stream' else 'other-preserved')
            families[family]+=1
            result['keys'][digest(key)]={'valueSHA256':digest(value),'expiresAtMS':expiry,'type':kind,'family':family}
        if int(cursor)==0:break
    result['observedAtMS']=int(time.time()*1000)
    result['keys']={k:v for k,v in result['keys'].items() if v['expiresAtMS']==-1 or v['expiresAtMS']>result['observedAtMS']}
    result['families']=families
    return result


def compare_redis(expected,actual):
    required={'observedAtMS','databases','keys','families'}
    if not required.issubset(expected) or not required.issubset(actual): raise ValueError('Complete Redis inventory required')
    if expected['databases']!=actual['databases']:raise ValueError('Redis database configuration differs')
    now=actual['observedAtMS']
    live={k:v for k,v in expected['keys'].items() if v['expiresAtMS']==-1 or v['expiresAtMS']>now}
    if live!=actual['keys']:raise ValueError('Redis state, stream/PEL or absolute expiry mismatch')
    families={'jwt:refresh:consumed:','jwt:revoked:','intake:identity-exchange:nonce:','stream','other-preserved'}
    if set(expected['families'])!=families or set(actual['families'])!=families:raise ValueError('Redis security family evidence missing')


class AuthProbe:
    def __init__(self,url,password): self.url=url;self.password=password
    def session(self,cookies=None):
        jar=http.cookiejar.CookieJar()
        for c in cookies or []: jar.set_cookie(copy.copy(c))
        return urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar)),jar
    def call(self,session,path,body=None,csrf=False):
        opener,jar=session;headers={}
        if csrf:
            status,value=self.call(session,'/csrf-token')
            if status!=200:raise ValueError('CSRF unavailable')
            headers['X-CSRF-Token']=value['data']['csrf_token']
        data=None if body is None else json.dumps(body).encode()
        if data is not None:headers['Content-Type']='application/json'
        request=urllib.request.Request(self.url+'/api/v1'+path,data=data,headers=headers)
        try:
            response=opener.open(request,timeout=20);status=response.status;raw=response.read()
        except urllib.error.HTTPError as e:status=e.code;raw=e.read()
        return status,json.loads(raw)
    def login(self):
        session=self.session();status,_=self.call(session,'/auth/login',{'tenantCode':'default','username':'admin','password':self.password})
        if status!=200:raise ValueError('Isolated auth login failed')
        if not {'access_token','refresh_token'}.issubset({c.name for c in session[1]}):raise ValueError('Actual session cookies required')
        return session
    def freeze_before(self):
        self.old=[];access=set()
        for _ in range(3):
            # Access JWTs have second-resolution claims and no jti; isolate logout cases.
            cookies=list(self.login()[1]);token=next(c.value for c in cookies if c.name=='access_token')
            if token in access:raise ValueError('Distinct actual access sessions required')
            access.add(token);self.old.append(cookies)
            time.sleep(1.1)
    def after_backup(self):
        a=self.session(self.old[0]);assert self.call(a,'/auth/refresh',{},csrf=True)[0]==200
        assert self.call(self.session(self.old[0]),'/auth/refresh',{},csrf=True)[0]==401
        assert self.call(self.session(self.old[1]),'/auth/logout',{},csrf=True)[0]==200
        assert self.call(self.session(self.old[1]),'/auth/me')[0]==401
        assert self.call(self.session(self.old[2]),'/auth/me')[0]==200
        self.old.append(list(self.login()[1]))
        return {'consumedAfterPoint':True,'revokedAfterPoint':True,'unconsumedValidBeforeRecovery':True,'afterPointSessionIssued':True}
    def recovered(self):
        for cookies in self.old:
            assert self.call(self.session(cookies),'/auth/me')[0]==401
            assert self.call(self.session(cookies),'/auth/refresh',{},csrf=True)[0]==401
        new=self.login();assert self.call(new,'/auth/me')[0]==200
        saved=list(new[1]);assert self.call(new,'/auth/refresh',{},csrf=True)[0]==200
        assert self.call(self.session(saved),'/auth/refresh',{},csrf=True)[0]==401
        body={'version':2,'audience':'itsm-intake','purpose':'create','provider':'task6-disabled','workspace':'isolated','subject':'fixture','channel':'test','eventId':'test','nonce':secrets.token_hex(16),'issuedAt':int(time.time()),'signature':'0'*64}
        status,response=self.call(self.session(),'/intake/identity-exchange',body,csrf=True)
        if status!=401 or 'provider or capability is unavailable' not in json.dumps(response):
            raise ValueError('Disabled identity provider was not explicitly rejected')
        return {'oldAccessRejected':4,'oldRefreshRejected':4,'newLoginAndAccessPassed':True,'newRefreshPassed':True,'newRefreshReplayRejected':True,'disabledProviderHTTP':status}


def validate_security_transition(before,after,policy):
    if not policy or policy.get('strategy')!='restore-all-redis-and-rotate-existing-JWT-secret' or policy.get('changedField')!='JWT_SECRET':
        raise ValueError('Explicit approved isolated security transition required')
    if set(k for k in set(before)|set(after) if before.get(k)!=after.get(k))!={'REDIS_PORT','JWT_SECRET'}:
        raise ValueError('Unapproved recovery configuration change')
    if len(after['JWT_SECRET'])<32 or before['JWT_SECRET']==after['JWT_SECRET'] or policy.get('oldDigest')!=digest(before['JWT_SECRET'].encode()) or policy.get('newDigest')!=digest(after['JWT_SECRET'].encode()):
        raise ValueError('Recovery security epoch digest mismatch')

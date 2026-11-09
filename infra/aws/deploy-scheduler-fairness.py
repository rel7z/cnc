#!/usr/bin/env python3
"""Stage scheduler fixes; apply the paused processor independently of the broker.

The controller restart gate refuses live enum jobs. Private tools are never
packaged, tested, inspected, or executed by this deployment helper.
"""
import argparse
import base64
import json
import os
from pathlib import Path
import subprocess
import time

from provision import aws, STATE_FILE, STATE_DIR, REGION, PROFILE
from release import prepare


BUILD = r'''
import hashlib,json,os,shutil,subprocess,tarfile,urllib.request
from pathlib import Path
p=json.loads(__import__('base64').b64decode(PAYLOAD))
release=Path('/opt/cnc/scheduler-releases')/p['digest'][:16]
release.mkdir(mode=0o700,parents=True,exist_ok=True)
archive=release/'source.tar.gz'
with urllib.request.urlopen(p['url'],timeout=120) as response:archive.write_bytes(response.read())
archive.chmod(0o600)
if hashlib.sha256(archive.read_bytes()).hexdigest()!=p['digest']:raise RuntimeError('Source checksum mismatch')
source=release/'source';source.mkdir(mode=0o700,exist_ok=True)
with tarfile.open(archive) as package:
 for member in package.getmembers():
  if member.name.startswith('processor/tools/') or not member.isfile():raise RuntimeError('Protected or nonregular source member')
 package.extractall(source,filter='data')
env=dict(os.environ,GOPATH='/opt/cnc/go-work',GOMODCACHE='/opt/cnc/go-work/pkg/mod',GOCACHE='/opt/cnc/go-cache',CNC_PROCESSOR_CGROUP_ROOT='/sys/fs/cgroup/cnc-scheduler-verification')
go='/opt/cnc/go/bin/go'
if shutil.which('gcc') is None:raise RuntimeError('Linux race verification requires gcc')
with (release/'build.log').open('w') as log:
 for directory,args in [('processor',['test','-race','./cluster','./api','./internal/distributed','-count=1']),('cnc-api',['test','-race','.','-run','TestDispatcherSharesMemory|TestBrokerAdmitsSmall|TestBrokerCountsOrdinary|TestGenericDispatcherUsesCapacity|TestWorkerCapacity','-count=1']),('processor',['build','-trimpath','-ldflags=-s -w','-o',str(release/'cnc-processor'),'./cmd/cnc-processor']),('cnc-api',['build','-trimpath','-ldflags=-s -w','-o',str(release/'cnc-server'),'./cmd/server'])]:
  command_env=dict(env,CGO_ENABLED='1' if args[0]=='test' else '0')
  subprocess.run([go,*args],cwd=source/directory,env=command_env,stdout=log,stderr=subprocess.STDOUT,check=True,timeout=600)
result={'release':str(release),'source_digest':p['digest'],'tests_passed':True,'binaries':{name:hashlib.sha256((release/name).read_bytes()).hexdigest() for name in ('cnc-processor','cnc-server')},'activated':False}
(release/'verified.json').write_text(json.dumps(result,indent=2));(release/'verified.json').chmod(0o600)
print(json.dumps(result))
'''


APPLY = r'''
import hashlib,json,os,shutil,sqlite3,subprocess,time,urllib.request
from pathlib import Path
p=json.loads(__import__('base64').b64decode(PAYLOAD));release=Path(p['release']);verified=json.loads((release/'verified.json').read_text())
v={}
for line in Path('/opt/cnc/processor.env').read_text().splitlines():
 if line and not line.startswith('#') and '=' in line:
  k,x=line.split('=',1);v[k]=x.strip('"')
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
def processor(path):
 req=urllib.request.Request('http://127.0.0.1:9191/v1/'+path,headers={'Authorization':'Bearer '+v['CNC_PROCESSOR_TOKEN']})
 with opener.open(req,timeout=10) as response:return json.load(response)
health=processor('health')
if health['enabled'] or health['counts'].get('running',0):raise RuntimeError('Processor must stay paused with no active attempts')
service='cnc-'+p['component'];target=Path('/opt/cnc-processor/cnc-processor') if p['component']=='processor' else Path('/opt/cnc/cnc-server')
def server_jobs():
 with opener.open('http://'+config['http_addr']+'/api/jobs',timeout=10) as response:return json.load(response)
if p['component']=='server':
 config=json.loads(Path('/opt/cnc/config.json').read_text())
 jobs=server_jobs()
 active=[j['id'] for j in jobs if j.get('status') in ('running','pending')]
 if active and not p.get('allow_active_enum_interruption'):raise RuntimeError('Controller restart refused: active jobs '+','.join(active))
 baseline_workers={w['id'] for w in (processor('cluster').get('workers') or []) if w.get('status') in ('online','busy')}
candidate=release/service
if hashlib.sha256(candidate.read_bytes()).hexdigest()!=verified['binaries'][service]:raise RuntimeError('Candidate checksum mismatch')
rollback=release/('rollback-'+str(time.time_ns()));rollback.mkdir(mode=0o700)
shutil.copy2(target,rollback/service)
if p['component']=='processor':
 with sqlite3.connect('/opt/cnc-processor/processor.db') as db,sqlite3.connect(rollback/'processor.db') as backup:db.backup(backup)
stage=target.with_suffix('.new');shutil.copy2(candidate,stage);stage.chmod(0o700);os.replace(stage,target)
try:
 subprocess.run(['systemctl','restart',service],check=True,timeout=60)
 deadline=time.monotonic()+45
 while True:
  try:
   current=processor('health')
   if current['enabled']:raise RuntimeError('Pause state changed')
   if current['counts']!=health['counts']:raise RuntimeError('Queue state changed')
   if p['component']=='server':
    server_jobs()
    online={w['id'] for w in (processor('cluster').get('workers') or []) if w.get('status') in ('online','busy')}
    if not baseline_workers.issubset(online):raise urllib.error.URLError('Waiting for workers to reconnect')
   break
  except (OSError,urllib.error.URLError):
   if time.monotonic()>deadline:raise
   time.sleep(1)
 if p['component']=='processor':
  runs=processor('runs') or []
  if any(r['Status']=='queued' and r['BlockedReason']!='processing is paused' for r in runs):raise RuntimeError('Paused queue display verification failed')
 if hashlib.sha256(target.read_bytes()).hexdigest()!=verified['binaries'][service]:raise RuntimeError('Installed checksum mismatch')
except BaseException:
 shutil.copy2(rollback/service,stage);os.replace(stage,target)
 subprocess.run(['systemctl','restart',service],check=True,timeout=60);raise
print(json.dumps({'applied':p['component'],'pause_preserved':True,'queue_preserved':True,'rollback':str(rollback),'binary_sha256':verified['binaries'][service]}))
'''


def send(script, instance, label):
    command = aws('ssm','send-command','--instance-ids',instance,'--document-name','AWS-RunShellScript',
                  '--comment',label,'--parameters',json.dumps({'commands':[script],'executionTimeout':['2400']}))['Command']['CommandId']
    print('SSM command:',command,flush=True)
    (STATE_DIR/'scheduler-pending.json').write_text(json.dumps({'command_id':command,'instance':instance}))
    while True:
        try:
            result=aws('ssm','get-command-invocation','--command-id',command,'--instance-id',instance)
        except RuntimeError as error:
            if 'InvocationDoesNotExist' in str(error):time.sleep(3);continue
            raise
        if result['Status']=='Success':
            print(result['StandardOutputContent'],flush=True)
            return json.loads(result['StandardOutputContent'].strip().splitlines()[-1])
        if result['Status'] in ('Failed','TimedOut','Cancelled'):
            raise RuntimeError(result.get('StandardErrorContent') or result['Status'])
        time.sleep(5)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply',choices=('processor','server'))
    parser.add_argument('--allow-active-enum-interruption',action='store_true')
    args=parser.parse_args()
    os.environ['PATH']=str(Path.home()/'.local/bin')+os.pathsep+os.environ.get('PATH','')
    state=json.loads(STATE_FILE.read_text());record=STATE_DIR/'scheduler-fairness.json'
    if args.apply:
        result=json.loads(record.read_text());result.update(component=args.apply,allow_active_enum_interruption=args.allow_active_enum_interruption)
        code=APPLY
    else:
        archive,digest,files=prepare()
        assert not any(str(p).startswith('processor/tools/') for p in files)
        bucket=state['release_bucket'];key='scheduler-fairness/'+digest+'.tar.gz'
        aws('s3api','put-object','--bucket',bucket,'--key',key,'--body',str(archive),'--server-side-encryption','AES256')
        url=subprocess.run(['aws','s3','presign',f's3://{bucket}/{key}','--expires-in','3600','--profile',PROFILE,'--region',REGION],capture_output=True,text=True,check=True).stdout.strip()
        result={'url':url,'digest':digest};code=BUILD
    payload=base64.b64encode(json.dumps(result).encode()).decode()
    result=send("python3 - <<'SCHEDULER'\n"+code.replace('PAYLOAD',repr(payload))+"\nSCHEDULER\n",state['nodes']['cnc-aws-server']['id'],'CNC scheduler fairness '+(args.apply or 'stage and verify'))
    if not args.apply:record.write_text(json.dumps(result,indent=2));record.chmod(0o600)


if __name__=='__main__':main()

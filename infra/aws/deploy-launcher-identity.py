#!/usr/bin/env python3
"""Repair dashboard launcher identity through Git, encrypted S3, and SSM."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import time

from provision import aws, ROOT, STATE_FILE, PROFILE, REGION

FILES = ['cnc-ui/components/dashboard/ToolsLauncher.tsx', 'infra/aws/configure-dashboard-audit.py', 'infra/aws/test_dashboard_audit.py', 'infra/aws/install-controller.sh', 'infra/aws/deploy-launcher-identity.py']

REMOTE = r'''

import hashlib,json,os,shutil,subprocess,tarfile,time,urllib.request,urllib.error
from pathlib import Path
root=Path('/opt/cnc'); source=root/'source/cnc-ui'
release=root/'launcher-releases'/REVISION; release.mkdir(parents=True,exist_ok=False)
release.chmod(0o700)
archive=release/'source.tar.gz'
with urllib.request.urlopen(DOWNLOAD,timeout=60) as response:archive.write_bytes(response.read())
if hashlib.sha256(archive.read_bytes()).hexdigest()!=SHA:raise RuntimeError('Release checksum mismatch')
archive.chmod(0o600)
extracted=release/'approved';extracted.mkdir(mode=0o700)
with tarfile.open(archive) as package:
 members=package.getmembers()
 if sorted(m.name for m in members)!=sorted(FILES) or not all(m.isfile() for m in members):raise RuntimeError('Unexpected release members')
 package.extractall(extracted,filter='data')
config=json.loads((root/'config.json').read_text());base='http://'+config['http_addr']
def api(path):return json.load(urllib.request.urlopen(base+path,timeout=10))
workers_before=sum(w.get('status')=='online' for w in api('/api/workers'))
jobs=api('/api/jobs');jobs=jobs.get('jobs',[]) if isinstance(jobs,dict) else jobs
if any(j.get('status') in ('queued','running','pending') for j in jobs):raise RuntimeError('Active jobs; retry after completion')
# Build separately, leaving the active standalone release untouched.
build=release/'build'
shutil.copytree(source,build,symlinks=True,ignore=shutil.ignore_patterns('node_modules','.next','.git','.env*'))
(build/'node_modules').symlink_to(source/'node_modules',target_is_directory=True)
for name in FILES:
 if name.startswith('cnc-ui/'):
  destination=build/name[len('cnc-ui/'):];destination.parent.mkdir(parents=True,exist_ok=True)
  shutil.copy2(extracted/name,destination)
node=Path((root/'node-path').read_text().strip())/'bin'
env=dict(os.environ,PATH=str(node)+':'+os.environ.get('PATH',''),GO_API_URL=base,NEXT_TELEMETRY_DISABLED='1')
log=release/'build.log'
with log.open('w') as output:
 subprocess.run([str(node/'node'),'--test','tests/gdrive-connect.test.mjs'],cwd=build,env=env,stdout=output,stderr=subprocess.STDOUT,check=True,timeout=60)
 subprocess.run([str(node/'npm'),'run','build','--','--webpack'],cwd=build,env=env,stdout=output,stderr=subprocess.STDOUT,check=True,timeout=600)
log.chmod(0o600)
target=build/'.next/standalone'
shutil.copytree(build/'.next/static',target/'.next/static',dirs_exist_ok=True)
if (build/'public').is_dir():shutil.copytree(build/'public',target/'public',dirs_exist_ok=True)
if not (target/'server.js').is_file():raise RuntimeError('Standalone build missing')
rollback=release/'rollback';rollback.mkdir(mode=0o700)
dropin=Path('/etc/systemd/system/cnc-ui.service.d/zzzz-launcher-identity.conf')
previous=dropin.read_bytes() if dropin.exists() else None
audit_paths=[root/'dashboard-audit.env',Path('/etc/systemd/system/cnc-server.service.d/zz-dashboard-audit.conf'),Path('/etc/systemd/system/cnc-ui.service.d/zz-dashboard-audit.conf')]
audit_before={str(p):p.read_bytes() if p.exists() else None for p in audit_paths}
for index,(name,data) in enumerate(audit_before.items()):
 if data is not None:(rollback/('audit-'+str(index))).write_bytes(data)
if previous is not None:(rollback/'dropin.conf').write_bytes(previous)
# Keep a recoverable source copy for every replaced UI file.
source_files=[name[len('cnc-ui/'):] for name in FILES if name.startswith('cnc-ui/')]
absent=[]
for name in source_files:
 old=source/name;backup=rollback/'source'/name
 if old.exists():
  if not old.is_file() or old.is_symlink():raise RuntimeError('Unsafe source path')
  backup.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(old,backup)
 else:absent.append(name)
(rollback/'absent.json').write_text(json.dumps(absent))
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kwargs):return None
def status(path):
 try:
  response=urllib.request.build_opener(NoRedirect()).open('http://127.0.0.1:3000'+path,timeout=5)
  return response.status,response.headers,response.read()
 except urllib.error.HTTPError as error:return error.code,error.headers,error.read()
try:
 subprocess.run(['python3',str(extracted/'infra/aws/configure-dashboard-audit.py')],check=True,stdout=subprocess.DEVNULL)
 dropin.parent.mkdir(parents=True,exist_ok=True)
 stage=dropin.with_suffix('.new')
 stage.write_text('[Service]\nExecStart=\nExecStart='+str(node/'node')+' '+str(target/'server.js')+'\nWorkingDirectory='+str(target)+'\nEnvironment=HOSTNAME=127.0.0.1\nEnvironment=PORT=3000\nEnvironment=GO_API_URL='+base+'\n')
 stage.chmod(0o600);stage.replace(dropin)
 subprocess.run(['systemctl','daemon-reload'],check=True)
 subprocess.run(['systemctl','restart','cnc-server'],check=True)
 subprocess.run(['systemctl','restart','cnc-ui'],check=True)
 for attempt in range(30):
  try:
   if status('/login')[0]==200:break
  except OSError:pass
  time.sleep(1)
 else:raise RuntimeError('Dashboard health check failed')
 code,headers,_=status('/drive-connect?ticket=invalid')
 if code!=303 or headers.get('Location')!='/drive-recover':raise RuntimeError('Expired link does not recover')
 code,_,body=status('/drive-recover')
 if code!=200 or b'Restore Google Drive connection' not in body or b'Sync now' not in body:raise RuntimeError('Recovery page missing')
 if status('/api/config/gdrive/auth-status')[0]!=401:raise RuntimeError('Dashboard API authentication changed')
 if status('/api/config/gdrive/callback')[0]!=400:raise RuntimeError('OAuth callback state validation changed')
 for attempt in range(30):
  workers_after=sum(w.get('status')=='online' for w in api('/api/workers'))
  if workers_after>=workers_before:break
  time.sleep(2)
 else:raise RuntimeError('Workers did not reconnect')
 def effective_environment(service):
  pid=subprocess.check_output(['systemctl','show',service,'--property=MainPID','--value'],text=True).strip()
  return dict(part.split(b'=',1) for part in Path('/proc/'+pid+'/environ').read_bytes().split(b'\0') if b'=' in part)
 server_env=effective_environment('cnc-server');ui_env=effective_environment('cnc-ui')
 server_key=server_env.get(b'CNC_DASHBOARD_AUDIT_SECRET',b'');ui_key=ui_env.get(b'CNC_DASHBOARD_AUDIT_SECRET',b'')
 if len(server_key)<32 or server_key!=ui_key:raise RuntimeError('Audit identity keys do not match')
 def post(url,data,headers=None):
  req=urllib.request.Request(url,data=json.dumps(data).encode(),headers={'Content-Type':'application/json',**(headers or {})},method='POST')
  try:response=urllib.request.urlopen(req,timeout=15)
  except urllib.error.HTTPError as error:response=error
  return response.status,response.headers,json.loads(response.read())
 ui_base='http://127.0.0.1:3000'
 access=dict(line.split('=',1) for line in (root/'login.env').read_text().splitlines() if '=' in line and not line.startswith('#'))
 code,headers,_=post(ui_base+'/api/auth/login',{'username':access['CNC_LOGIN_USERNAME'],'password':access['CNC_LOGIN_PASSWORD']},{'Origin':ui_base})
 if code!=200:raise RuntimeError('Dashboard sign-in health failed')
 cookie=headers.get('Set-Cookie','').split(';',1)[0]
 probe={'tool_id':'enum','options':{'threads':0}}
 signed_code,_,result=post(ui_base+'/api/tools/launch',probe,{'Cookie':cookie,'Origin':ui_base})
 if signed_code!=400 or 'threads' not in result.get('error',''):raise RuntimeError('Authenticated launcher probe failed')
 unsigned_code,_,_=post(base+'/api/tools/launch',probe)
 if unsigned_code!=401:raise RuntimeError('Unsigned launcher request was not rejected')
 registry=urllib.request.Request(ui_base+'/api/tools',headers={'Cookie':cookie})
 tools=json.load(urllib.request.urlopen(registry,timeout=10))
 if not tools:raise RuntimeError('Tool registry is empty')
 for name in source_files:
  destination=source/name;destination.parent.mkdir(parents=True,exist_ok=True)
  shutil.copy2(extracted/'cnc-ui'/name,destination)
 for name in ('configure-dashboard-audit.py','install-controller.sh'):
  destination=root/'source/infra/aws'/name;destination.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(extracted/'infra/aws'/name,destination)
 (release/'DEPLOYED').write_text(REVISION+'\n')
 print(json.dumps({'revision':REVISION,'protected_api_http':401,'workers_online':workers_after,'drive_authorized':api('/api/config/gdrive/auth-status').get('authorized'),'rollback':str(rollback),'backend_restarted':True,'audit_keys_match':True,'authenticated_launcher_probe_http':signed_code,'unsigned_launcher_probe_http':unsigned_code,'registry_tools':len(tools),'tools_executed':False}))
except Exception:
 for name,data in audit_before.items():
  p=Path(name)
  if data is None:p.unlink(missing_ok=True)
  else:p.write_bytes(data);p.chmod(0o600)
 if previous is None:dropin.unlink(missing_ok=True)
 else:dropin.write_bytes(previous)
 for name in source_files:
  backup=rollback/'source'/name;destination=source/name
  if backup.exists():shutil.copy2(backup,destination)
  elif name in absent:destination.unlink(missing_ok=True)
 subprocess.run(['systemctl','daemon-reload'],check=False);subprocess.run(['systemctl','restart','cnc-server'],check=False);subprocess.run(['systemctl','restart','cnc-ui'],check=False)
 raise

'''

def main():
    state=json.loads(STATE_FILE.read_text())
    instance=state['nodes']['cnc-aws-server']['id']
    with tempfile.TemporaryDirectory(prefix='cnc-drive-release-') as directory:
        env=dict(os.environ,GIT_INDEX_FILE=str(Path(directory)/'index'))
        def git(*args):return subprocess.check_output(['git',*args],cwd=ROOT,env=env,text=True).strip()
        git('read-tree','--empty');git('add','--',*FILES)
        revision=git('commit-tree',git('write-tree'),'-m','Repair AWS dashboard launcher audit identity wiring')
        git('update-ref','refs/heads/codex/launcher-identity-aws',revision)
        archive=Path(directory)/'source.tar.gz'
        with tarfile.open(archive,'w:gz') as package:
            for name in FILES:
                p=ROOT/name
                if p.is_symlink() or not p.is_file():raise RuntimeError('Invalid release file '+name)
                package.add(p,arcname=name,recursive=False)
        sha=hashlib.sha256(archive.read_bytes()).hexdigest()
        bucket=state['release_bucket'];key='releases/launcher-identity-'+revision+'.tar.gz'
        aws('s3api','put-object','--bucket',bucket,'--key',key,'--body',str(archive),'--server-side-encryption','AES256')
        url=subprocess.check_output(['aws','s3','presign','s3://'+bucket+'/'+key,'--expires-in','1800','--profile',PROFILE,'--region',REGION],text=True).strip()
        values={'REVISION':revision,'SHA':sha,'DOWNLOAD':url,'FILES':FILES}
        code='\n'.join(k+'='+repr(v) for k,v in values.items())+'\n'+REMOTE
        import shlex
        command='python3 - <<\'CNC_DRIVE_DEPLOY\'\n'+code+'\nCNC_DRIVE_DEPLOY\n'
        result=aws('ssm','send-command','--instance-ids',instance,'--document-name','AWS-RunShellScript','--comment','CNC launcher identity repair','--parameters',json.dumps({'commands':[command],'executionTimeout':['900']}))
        command_id=result['Command']['CommandId']
        print(json.dumps({'revision':revision,'command_id':command_id}),flush=True)
        while True:
            try:result=aws('ssm','get-command-invocation','--command-id',command_id,'--instance-id',instance)
            except RuntimeError as error:
                if 'InvocationDoesNotExist' in str(error):time.sleep(3);continue
                raise
            if result['Status'] not in ('Pending','InProgress','Delayed'):
                print(json.dumps({k:result.get(k) for k in ('Status','StandardOutputContent','StandardErrorContent')},indent=2))
                if result['Status']!='Success':raise RuntimeError('Deployment failed; inspect the release build log')
                break
            time.sleep(5)

if __name__=='__main__':main()

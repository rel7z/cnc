#!/usr/bin/env python3
"""Deploy matched enum/controller/worker/dashboard builds; never access private tools.

Requires the verified Linux binaries in /tmp/cnc-version-*-linux and a completed
cnc-ui/.next/standalone production build. Retains rollback files and preserves
runtime configuration, Drive credentials, processor state and private wrappers.
"""
import concurrent.futures
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tarfile

ROOT=Path(__file__).resolve().parents[2]
STAGE=Path('/tmp/cnc-version-release')
HOSTS=['cnc-linode-server']+[f'cnc-linode-worker-{i}' for i in range(1,7)]


def remote(host,script,**values):
    prefix=''.join(key+'='+repr(value)+'\n' for key,value in values.items())
    return subprocess.check_output(['ssh','-F',str(ROOT/'.linode/ssh_config'),'-o','ConnectTimeout=10',host,'python3 -'],input=(prefix+script).encode()).decode()


def upload(host,path,destination):
    subprocess.run(['scp','-q','-F',str(ROOT/'.linode/ssh_config'),str(path),host+':'+destination],check=True)


PREPARE=r'''
import json,shutil,sqlite3,urllib.request
from pathlib import Path
release=Path(RELEASE)
settings=dict(l.split('=',1) for l in Path('/opt/cnc/processor.env').read_text().splitlines() if '=' in l and not l.startswith('#'))
headers={'Authorization':'Bearer '+settings['CNC_PROCESSOR_TOKEN'].strip('"'),'Content-Type':'application/json'}
def api(path,body=None):
 request=urllib.request.Request('http://127.0.0.1:9191/v1/'+path,data=None if body is None else json.dumps(body).encode(),headers=headers,method='GET' if body is None else 'PUT')
 with urllib.request.urlopen(request,timeout=15) as r:return json.load(r)
initial=api('health')['enabled']
(release/'previous-state.json').write_text(json.dumps({'enabled':initial}))
api('processing-state',{'enabled':False})
try:
 with urllib.request.urlopen('http://192.168.145.81:8080/api/workers',timeout=15) as r:workers=json.load(r)
 if api('health')['counts']['running'] or len(workers)!=6 or any(w['current_load'] for w in workers):raise RuntimeError('Expected six idle workers; rollout not activated')
 backup=release/'rollback-state';backup.mkdir(exist_ok=True)
 for source in ['/opt/cnc/config.json','/opt/cnc-processor/config.json']:
  destination=backup/source.lstrip('/');destination.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(source,destination)
 with sqlite3.connect('/opt/cnc-processor/processor.db') as source,sqlite3.connect(backup/'processor.db') as destination:source.backup(destination)
 with urllib.request.urlopen('http://192.168.145.81:8080/api/jobs',timeout=15) as r:(backup/'previous-jobs.json').write_bytes(r.read())
 # Check the signing secret before stopping any service. Do not print it.
 settings=dict(l.split('=',1) for l in Path('/opt/cnc/login.env').read_text().splitlines() if '=' in l and not l.startswith('#'))
 if len(settings.get('CNC_SESSION_SECRET','').strip('"').strip("'"))<32:raise RuntimeError('Missing dashboard signing secret')
 print(json.dumps({'idle_workers':6,'intake_paused':True,'state_backed_up':True}))
except Exception:
 api('processing-state',{'enabled':initial});raise
'''

ACTIVATE=r'''
import hashlib,json,shutil,subprocess,tarfile
from pathlib import Path
release=Path(RELEASE);backup=release/'rollback';backup.mkdir(exist_ok=True)
manifest=json.loads((release/'manifest.json').read_text())
mapping={'enum':'/opt/cnc/tools/cnc-enum','worker':'/opt/cnc/cnc-worker-linux' if SERVER else '/opt/cnc/cnc-worker'}
if SERVER:mapping['server']='/opt/cnc/cnc-server'
for name,dest in mapping.items():
 assert hashlib.sha256((release/name).read_bytes()).hexdigest()==manifest['binaries'][name]
 saved=backup/Path(dest).name
 if not saved.exists():shutil.copy2(dest,saved)
changed_files=['/opt/cnc/enum-audit.env','/etc/systemd/system/cnc-server.service.d/zz-enum-version-pipeline.conf','/etc/systemd/system/cnc-ui.service.d/zz-enum-version-pipeline.conf'] if SERVER else []
for path in changed_files:
 destination=Path(path);saved=backup/path.lstrip('/')
 if destination.exists() and not saved.exists():saved.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(destination,saved)
if SERVER:
 archive=release/'ui.tar.gz';assert hashlib.sha256(archive.read_bytes()).hexdigest()==manifest['ui_sha256']
 with tarfile.open(archive) as tar:
  assert all(m.isfile() or m.isdir() for m in tar.getmembers())
  tar.extractall(release,filter='data')
 subprocess.run(['systemctl','stop','cnc-ui'],check=True)
service='cnc-server' if SERVER else 'cnc-worker'
subprocess.run(['systemctl','stop',service],check=True)
for name,dest in mapping.items():
 destination=Path(dest);temporary=destination.with_name(destination.name+'.enum-new');shutil.copy2(release/name,temporary);temporary.chmod(0o700);temporary.replace(destination)
 assert hashlib.sha256(destination.read_bytes()).hexdigest()==manifest['binaries'][name]
if SERVER:
 settings=dict(l.split('=',1) for l in Path('/opt/cnc/login.env').read_text().splitlines() if '=' in l and not l.startswith('#'))
 secret=settings['CNC_SESSION_SECRET'].strip('"').strip("'")
 assert len(secret)>=32 and '\n' not in secret
 audit=Path('/opt/cnc/enum-audit.env');audit.write_text('CNC_DASHBOARD_AUDIT_SECRET='+secret+'\n');audit.chmod(0o600)
 for unit,content in [
 ('cnc-server','[Service]\nEnvironmentFile=/opt/cnc/enum-audit.env\n'),
 ('cnc-ui','[Service]\nEnvironmentFile=/opt/cnc/enum-audit.env\nEnvironment=GO_API_URL=http://192.168.145.81:8080\nExecStart=\nExecStart=/opt/cnc/node-runtime/node-v22.23.3-linux-x64/bin/node '+str(release/'ui/.next/standalone/server.js')+'\nWorkingDirectory='+str(release/'ui/.next/standalone')+'\n')]:
  path=Path('/etc/systemd/system/'+unit+'.service.d/zz-enum-version-pipeline.conf');path.parent.mkdir(parents=True,exist_ok=True);path.write_text(content)
 subprocess.run(['systemctl','daemon-reload'],check=True)
subprocess.run(['systemctl','start',service],check=True)
if SERVER:subprocess.run(['systemctl','start','cnc-ui'],check=True)
print(json.dumps({'binaries_verified':len(mapping),'service':service,'active':subprocess.check_output(['systemctl','is-active',service],text=True).strip()}))
'''

ROLLBACK=r'''
import shutil,subprocess
from pathlib import Path
release=Path(RELEASE);backup=release/'rollback'
service='cnc-server' if SERVER else 'cnc-worker'
subprocess.run(['systemctl','stop',service],check=True)
if SERVER:subprocess.run(['systemctl','stop','cnc-ui'],check=True)
mapping={'cnc-enum':'/opt/cnc/tools/cnc-enum','cnc-worker-linux' if SERVER else 'cnc-worker':'/opt/cnc/cnc-worker-linux' if SERVER else '/opt/cnc/cnc-worker'}
if SERVER:mapping['cnc-server']='/opt/cnc/cnc-server'
for name,path in mapping.items():
 if (backup/name).exists():shutil.copy2(backup/name,path)
if SERVER:
 for path in ['/opt/cnc/enum-audit.env','/etc/systemd/system/cnc-server.service.d/zz-enum-version-pipeline.conf','/etc/systemd/system/cnc-ui.service.d/zz-enum-version-pipeline.conf']:
  saved=backup/path.lstrip('/');destination=Path(path)
  if saved.exists():shutil.copy2(saved,destination)
  elif destination.exists():destination.unlink()
 subprocess.run(['systemctl','daemon-reload'],check=True)
subprocess.run(['systemctl','start',service],check=True)
if SERVER:subprocess.run(['systemctl','start','cnc-ui'],check=True)
print('Previous deployment restored')
'''

RESTORE=r'''
import json,time,urllib.request
from pathlib import Path
release=Path(RELEASE)
settings=dict(l.split('=',1) for l in Path('/opt/cnc/processor.env').read_text().splitlines() if '=' in l and not l.startswith('#'))
initial=json.loads((release/'previous-state.json').read_text())['enabled']
request=urllib.request.Request('http://127.0.0.1:9191/v1/processing-state',data=json.dumps({'enabled':initial}).encode(),headers={'Authorization':'Bearer '+settings['CNC_PROCESSOR_TOKEN'].strip('"'),'Content-Type':'application/json'},method='PUT')
with urllib.request.urlopen(request,timeout=15) as r:r.read()
for attempt in range(60):
 with urllib.request.urlopen('http://192.168.145.81:8080/api/workers',timeout=15) as r:workers=json.load(r)
 if len(workers)==6 and all(w['status']=='online' for w in workers):break
 time.sleep(.5)
else:raise RuntimeError('Six workers did not reconnect')
print(json.dumps({'workers_online':6,'processor_intake_restored':initial}))
'''

HEALTH=r'''
import json,time,urllib.request,urllib.error,subprocess
for attempt in range(30):
 try:
  for service in ['cnc-server','cnc-ui','cnc-processor']:
   assert subprocess.check_output(['systemctl','is-active',service],text=True).strip()=='active'
  with urllib.request.urlopen('http://127.0.0.1:3000/login',timeout=5) as response:assert response.status==200
  request=urllib.request.Request('http://127.0.0.1:3000/api/tools/launch',data=b'{}',headers={'Content-Type':'application/json'})
  try:urllib.request.urlopen(request,timeout=5).close();raise RuntimeError('Unauthenticated launch was accepted')
  except urllib.error.HTTPError as error:assert error.code==401
  with urllib.request.urlopen('http://192.168.145.81:8080/api/workers',timeout=5) as response:workers=json.load(response)
  assert len(workers)==6 and all(worker['status']=='online' for worker in workers)
  print(json.dumps({'dashboard_login':200,'unauthenticated_launch':401,'workers_online':6,'services_active':True}));break
 except Exception:
  if attempt==29:raise
  time.sleep(1)
'''


def main():
    STAGE.mkdir(exist_ok=True);STAGE.chmod(0o700)
    binaries={}
    for name in ['server','worker','enum']:
        path=Path('/tmp/cnc-version-'+name+'-linux')
        binaries[name]=hashlib.sha256(path.read_bytes()).hexdigest()
    with tarfile.open(STAGE/'ui.tar.gz','w:gz') as tar:
        for source,destination in [(ROOT/'cnc-ui/.next/standalone','ui/.next/standalone'),(ROOT/'cnc-ui/.next/static','ui/.next/standalone/.next/static'),(ROOT/'cnc-ui/public','ui/.next/standalone/public')]:
            assert source.is_dir()
            for path in sorted(source.rglob('*')):
                if path.is_symlink():raise RuntimeError('Unexpected build symlink')
                if path.is_file():tar.add(path,arcname=str(Path(destination)/path.relative_to(source)),recursive=False)
    ui_digest=hashlib.sha256((STAGE/'ui.tar.gz').read_bytes()).hexdigest()
    release='cms-version-'+hashlib.sha256(json.dumps([binaries,ui_digest],sort_keys=True).encode()).hexdigest()[:16]
    directory='/opt/cnc/queue-releases/'+release
    manifest={'release':release,'binaries':binaries,'ui_sha256':ui_digest,'workers':6}
    (STAGE/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    if sys.argv[1:] == ['--package-only']:
        print(json.dumps(dict(manifest, deployed=False, package=str(STAGE/'ui.tar.gz'))))
        return
    if sys.argv[1:]:
        raise RuntimeError('Unknown rollout arguments')
    def stage(host):
        remote(host,'from pathlib import Path\np=Path(RELEASE);p.mkdir(parents=True,exist_ok=True);p.chmod(0o700)',RELEASE=directory)
        names=['server','worker','enum'] if host==HOSTS[0] else ['worker','enum']
        for name in names:upload(host,Path('/tmp/cnc-version-'+name+'-linux'),directory+'/'+name)
        upload(host,STAGE/'manifest.json',directory+'/manifest.json')
        if host==HOSTS[0]:upload(host,STAGE/'ui.tar.gz',directory+'/ui.tar.gz')
        print(host,'release staged',flush=True)
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:list(pool.map(stage,HOSTS))
    print(remote(HOSTS[0],PREPARE,RELEASE=directory),flush=True)
    changed=[]
    try:
        for host in HOSTS:
            changed.append(host)
            print(host,remote(host,ACTIVATE,RELEASE=directory,SERVER=host==HOSTS[0]),flush=True)
        print(remote(HOSTS[0],HEALTH),flush=True)
    except Exception:
        for host in reversed(changed):print(remote(host,ROLLBACK,RELEASE=directory,SERVER=host==HOSTS[0]),flush=True)
        raise
    finally:print(remote(HOSTS[0],RESTORE,RELEASE=directory),flush=True)
    manifest.update({'deployed':True,'kiro_accessed':False,'private_tools_accessed':False})
    (STAGE/'deployment.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print(json.dumps(manifest),flush=True)


if __name__=='__main__':main()

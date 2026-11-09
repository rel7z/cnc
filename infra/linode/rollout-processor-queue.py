#!/usr/bin/env python3
"""Coordinated queue-v2 rollout using an explicit, task-approved tool manifest.

Build the three Linux binaries into /tmp/cnc-queue-release first. No broad Git
snapshot or private-tools discovery is used. Previous binaries, config and DB
are retained for rollback; old worker caches are untouched.
"""
import hashlib
import json
from pathlib import Path
import shlex
import subprocess
import tarfile
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
ARTIFACTS = Path('/tmp/cnc-queue-release')
APPROVED_TOOLS = [
    'tools/3-tools/common.py', 'tools/3-tools/task_a.py',
    'tools/3-tools/task_b.py', 'tools/1-tools/x.py',
    'tools/3-tools/task_a_run.py', 'tools/3-tools/task_b_run.py',
]


def ssh(host, script, data=None):
    return subprocess.check_output(['ssh', '-F', str(ROOT/'.linode/ssh_config'),
        '-o', 'ConnectTimeout=10', host, 'set -eu\n'+script], input=data).decode()


def upload(host, local, remote):
    subprocess.run(['scp', '-F', str(ROOT/'.linode/ssh_config'), str(local),
                    host + ':' + remote], check=True)


def package():
    ARTIFACTS.mkdir(parents=True,exist_ok=True)
    ARTIFACTS.chmod(0o700)
    files = {name: ARTIFACTS/name for name in ('cnc-server', 'cnc-worker', 'cnc-processor')}
    files['processor/config.json'] = ROOT/'processor/config.json'
    files['source/cnc-ui/components/dashboard/ProcessorSettings.tsx'] = ROOT/'cnc-ui/components/dashboard/ProcessorSettings.tsx'
    for name in APPROVED_TOOLS:
        files['processor/'+name] = ROOT/'processor'/name
    for directory in ('cluster', 'api', 'cmd/cnc-processor', 'internal/store', 'internal/watcher', 'internal/distributed', 'internal/ui'):
        base = ROOT/'processor'/directory
        for file in base.glob('*.go'):
            if not file.name.endswith('_test.go'):
                files['source/processor/'+str(file.relative_to(ROOT/'processor'))] = file
    for name in ('go.mod', 'go.sum', 'internal/store/schema.sql', 'internal/ui/index.html',
                 'cluster/python/processor_publish.py', 'QUEUE_ARCHITECTURE.md'):
        files['source/processor/'+name] = ROOT/'processor'/name
    manifest = {name: hashlib.sha256(path.read_bytes()).hexdigest() for name,path in files.items()}
    release = 'queue-v2-' + hashlib.sha256(json.dumps(manifest,sort_keys=True).encode()).hexdigest()[:16]
    archive = ARTIFACTS/'release.tar.gz'
    with tarfile.open(archive, 'w:gz') as tar:
        for name,path in files.items():
            if path.is_symlink():
                raise RuntimeError('Manifest requires regular files: '+str(path))
            tar.add(path, arcname=name, recursive=False)
    archive.chmod(0o600)
    (ARTIFACTS/'manifest.json').write_text(json.dumps({'release':release,'files':manifest},indent=2))
    (ARTIFACTS/'manifest.json').chmod(0o600)
    return release,archive


PREPARE_UI = r'''
import shutil,subprocess,os,json
from pathlib import Path
release=Path(RELEASE)
original=Path('/opt/cnc/sync-tool-source/cnc-ui')
ui=release/'ui'
shutil.copytree(original,ui,ignore=shutil.ignore_patterns('node_modules','.next','.git','*.tsbuildinfo'))
(ui/'node_modules').symlink_to(original/'node_modules',target_is_directory=True)
shutil.copy2(release/'source/cnc-ui/components/dashboard/ProcessorSettings.tsx',ui/'components/dashboard/ProcessorSettings.tsx')
node=Path('/opt/cnc/node-path').read_text().strip()+'/bin'
env=dict(os.environ,PATH=node+':'+os.environ['PATH'],NEXT_TELEMETRY_DISABLED='1',GO_API_URL='http://'+json.loads(Path('/opt/cnc/config.json').read_text())['http_addr'])
subprocess.run(['npm','run','build','--','--webpack'],cwd=ui,env=env,check=True)
shutil.copytree(ui/'.next/static',ui/'.next/standalone/.next/static',dirs_exist_ok=True)
if (ui/'public').exists():shutil.copytree(ui/'public',ui/'.next/standalone/public',dirs_exist_ok=True)
print('Prepared queue dashboard')
'''

PREPARE_SERVER = r'''
import json,sqlite3,shutil,subprocess,time,urllib.request
from pathlib import Path
release=Path(RELEASE)
app=Path('/opt/cnc-processor')
settings=dict(line.split('=',1) for line in Path('/opt/cnc/processor.env').read_text().splitlines() if '=' in line and not line.startswith('#'))
headers={'Authorization':'Bearer '+settings['CNC_PROCESSOR_TOKEN'].strip('"'),'Content-Type':'application/json'}
def api(path,body=None):
 r=urllib.request.Request('http://127.0.0.1:9191/v1/'+path,data=None if body is None else json.dumps(body).encode(),headers=headers,method='GET' if body is None else 'PUT')
 return json.load(urllib.request.urlopen(r,timeout=15))
initial=api('health')['enabled']
api('processing-state',{'enabled':False})
deadline=time.monotonic()+120
while api('health')['counts']['running']:
 if time.monotonic()>deadline:raise RuntimeError('Jobs still running; processor remains paused, no binaries replaced')
 time.sleep(2)
server=json.loads(Path('/opt/cnc/config.json').read_text())
workers=json.load(urllib.request.urlopen('http://'+server['http_addr']+'/api/workers',timeout=10))
if any(w['current_load'] for w in workers):raise RuntimeError('CNC workers are busy; no binaries replaced')
backup=release/'rollback';backup.mkdir()
paths=['/opt/cnc/cnc-server','/opt/cnc/cnc-worker-linux','/opt/cnc-processor/cnc-processor','/opt/cnc-processor/config.json','/etc/systemd/system/cnc-ui.service.d/queue-v2.conf']
paths += ['/opt/cnc-processor/'+name for name in TOOLS]
existing={}
for path in paths:
 p=Path(path);existing[path]=p.exists()
 if p.exists():
  dst=backup/path.lstrip('/');dst.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(p,dst)
with sqlite3.connect(app/'processor.db') as db,sqlite3.connect(backup/'processor.db') as dest:db.backup(dest)
(backup/'state.json').write_text(json.dumps({'enabled':initial,'files':existing}))
subprocess.run(['systemctl','stop','cnc-processor'],check=True)
print('Paused and backed up processor; no active jobs')
'''

ACTIVATE_SERVER = r'''
import json,shutil,subprocess,time,urllib.request
from pathlib import Path
release=Path(RELEASE);app=Path('/opt/cnc-processor')
for relative in ['config.json']+TOOLS:
 dst=app/relative;dst.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(release/'processor'/relative,dst);dst.chmod(0o600)
for name,destination in [('cnc-server','/opt/cnc/cnc-server'),('cnc-worker','/opt/cnc/cnc-worker-linux'),('cnc-processor','/opt/cnc-processor/cnc-processor')]:
 dst=Path(destination);temporary=dst.with_suffix('.queue-new');shutil.copy2(release/name,temporary);temporary.chmod(0o700);temporary.replace(dst)
subprocess.run(['systemctl','restart','cnc-server'],check=True)
subprocess.run(['systemctl','start','cnc-processor'],check=True)
node=Path('/opt/cnc/node-path').read_text().strip()+'/bin/node'
ui=release/'ui/.next/standalone'
drop=Path('/etc/systemd/system/cnc-ui.service.d/queue-v2.conf');drop.parent.mkdir(parents=True,exist_ok=True)
drop.write_text('[Service]\nExecStart=\nExecStart='+node+' '+str(ui/'server.js')+'\nWorkingDirectory='+str(ui)+'\n')
subprocess.run(['systemctl','daemon-reload'],check=True);subprocess.run(['systemctl','restart','cnc-ui'],check=True)
print('Activated queue controller, CNC server and dashboard')
'''

ROLLBACK_SERVER = r'''
import json,shutil,subprocess
from pathlib import Path
release=Path(RELEASE);backup=release/'rollback';state=json.loads((backup/'state.json').read_text())
subprocess.run(['systemctl','stop','cnc-processor','cnc-server','cnc-ui'],check=True)
for path,existed in state['files'].items():
 dst=Path(path)
 if existed:dst.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(backup/path.lstrip('/'),dst)
 else:
  if '/tools/' not in path:dst.unlink(missing_ok=True)
app=Path('/opt/cnc-processor')
for suffix in ('-wal','-shm'):(app/('processor.db'+suffix)).unlink(missing_ok=True)
shutil.copy2(backup/'processor.db',app/'processor.db')
subprocess.run(['systemctl','daemon-reload'],check=True)
subprocess.run(['systemctl','start','cnc-server','cnc-processor','cnc-ui'],check=True)
print('Restored previous server deployment and database; processing paused')
'''


def remote_python(host, source, directory):
    prefix='RELEASE='+repr(directory)+'\nTOOLS='+repr(APPROVED_TOOLS)+'\n'
    return ssh(host,'python3 -', (prefix+source).encode())


def main():
    release,archive=package()
    nodes=json.loads((ROOT/'.linode/state.json').read_text())['servers']
    server=next(name for name in nodes if name.endswith('-server'))
    workers=sorted(name for name in nodes if '-worker-' in name)
    if len(workers)!=6:raise RuntimeError('Expected exactly six recorded workers')
    directory='/opt/cnc/queue-releases/'+release
    ssh(server,'mkdir -p '+shlex.quote(directory)+'; chmod 700 '+shlex.quote(directory))
    upload(server,archive,directory+'/release.tar.gz')
    ssh(server,'tar -xzf '+shlex.quote(directory+'/release.tar.gz')+' -C '+shlex.quote(directory))
    if '--controller-only' in sys.argv[1:]:
        source = r"""
import json,shutil,subprocess,urllib.request
from pathlib import Path
release=Path(RELEASE)
settings=dict(line.split('=',1) for line in Path('/opt/cnc/processor.env').read_text().splitlines() if '=' in line and not line.startswith('#'))
request=urllib.request.Request('http://127.0.0.1:9191/v1/health',headers={'Authorization':'Bearer '+settings['CNC_PROCESSOR_TOKEN'].strip('"')})
health=json.load(urllib.request.urlopen(request,timeout=10))
if health['enabled'] or health['counts']['running']:raise RuntimeError('Controller patch requires paused, idle processor')
backup=release/'rollback';backup.mkdir()
shutil.copy2('/opt/cnc-processor/cnc-processor',backup/'cnc-processor')
subprocess.run(['systemctl','stop','cnc-processor'],check=True)
destination=Path('/opt/cnc-processor/cnc-processor')
try:
 temporary=destination.with_suffix('.queue-new');shutil.copy2(release/'cnc-processor',temporary);temporary.chmod(0o700);temporary.replace(destination)
 subprocess.run(['systemctl','start','cnc-processor'],check=True)
except Exception:
 shutil.copy2(backup/'cnc-processor',destination);subprocess.run(['systemctl','start','cnc-processor'],check=True);raise
print(json.dumps({'release':str(release),'state':'controller-patched-paused'}))
"""
        print(remote_python(server,source,directory),flush=True)
        return
    print(remote_python(server,PREPARE_UI,directory),flush=True)
    print(remote_python(server,PREPARE_SERVER,directory),flush=True)
    updated=[]
    try:
        for worker in workers:
            backup='/opt/cnc/queue-rollbacks/'+release
            ssh(worker,'mkdir -p '+shlex.quote(backup)+'; cp -p /opt/cnc/cnc-worker '+shlex.quote(backup+'/cnc-worker'))
            upload(worker,ARTIFACTS/'cnc-worker','/opt/cnc/cnc-worker.queue-new')
            # Stop all workers before activation to avoid mixing protocol versions.
            ssh(worker,'systemctl stop cnc-worker; chmod 700 /opt/cnc/cnc-worker.queue-new; mv /opt/cnc/cnc-worker.queue-new /opt/cnc/cnc-worker')
            updated.append(worker)
        print(remote_python(server,ACTIVATE_SERVER,directory),flush=True)
        for worker in workers:ssh(worker,'systemctl start cnc-worker')
        print(json.dumps({'release':release,'workers':workers,'rollback':directory+'/rollback','state':'deployed-paused'}),flush=True)
    except Exception:
        for worker in updated:
            backup='/opt/cnc/queue-rollbacks/'+release+'/cnc-worker'
            ssh(worker,'systemctl stop cnc-worker; cp -p '+shlex.quote(backup)+' /opt/cnc/cnc-worker; systemctl start cnc-worker')
        print(remote_python(server,ROLLBACK_SERVER,directory),flush=True)
        raise


if __name__=='__main__':main()

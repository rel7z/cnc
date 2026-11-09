#!/usr/bin/env python3
"""Core-only manifest rollout. Never reads, rebuilds or ships private tools."""
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[2]
WORKERS = [f"cnc-linode-worker-{i}" for i in range(1, 7)]


def ssh(host, command, code=None):
    return subprocess.check_output(["ssh", "-F", str(ROOT/".linode/ssh_config"),
        "-o", "ConnectTimeout=10", "-o", "ControlMaster=no", "-o", "ControlPath=none",
        host, command], input=code.encode() if code is not None else None).decode()


def upload(host, source, destination):
    subprocess.run(["scp", "-F", str(ROOT/".linode/ssh_config"), "-o", "ControlMaster=no",
        "-o", "ControlPath=none", str(source), host+":"+destination], check=True)


COMMON = '''
from pathlib import Path
import json,sqlite3,subprocess,shutil,time,urllib.request
release=Path(RELEASE)
env={}
for line in Path('/opt/cnc/processor.env').read_text().splitlines():
 k,s,v=line.partition('=')
 if s:env[k]=v.strip('"')
def api(path,body=None,base='http://127.0.0.1:9191/v1/'):
 request=urllib.request.Request(base+path,headers={'Authorization':'Bearer '+env['CNC_PROCESSOR_TOKEN'],'Content-Type':'application/json'},data=json.dumps(body).encode() if body is not None else None,method='GET' if body is None else 'PUT')
 with urllib.request.urlopen(request,timeout=15) as r:return json.load(r)
def workers():return api('api/processor/cluster/workers',base=env['CNC_PROCESSOR_BROKER_URL'].rstrip('/')+'/')
dropin=Path('/etc/systemd/system/cnc-processor.service.d/manifest-pins.conf')
'''

PREPARE = '''
initial=api('health')['enabled'];api('processing-state',{'enabled':False})
try:
 deadline=time.monotonic()+180
 while api('health')['counts']['running'] or any(w['current_load'] for w in workers()):
  if time.monotonic()>deadline:raise RuntimeError('Work did not drain; binaries unchanged')
  time.sleep(2)
 w=workers();assert len(w)==6 and all(x['status']=='online' for x in w)
 backup=release/'rollback';backup.mkdir()
 for name,source in [('cnc-processor','/opt/cnc-processor/cnc-processor'),('cnc-worker-linux','/opt/cnc/cnc-worker-linux'),('config.json','/opt/cnc-processor/config.json')]:shutil.copy2(source,backup/name)
 if dropin.exists():shutil.copy2(dropin,backup/'manifest-pins.conf')
 config=json.loads(Path('/opt/cnc-processor/config.json').read_text())
 with sqlite3.connect('/opt/cnc-processor/processor.db') as db:
  versions=dict(db.execute('SELECT program_id,version FROM processor_programs'))
 assert set(config['programs'])=={'task-a','task-b'},'Unexpected production registry; inspect core metadata before rollout'
 state={'enabled':initial,'versions':{id:versions[id] for id in config['programs']},'pinned':sorted(config['programs'])}
 (backup/'state.json').write_text(json.dumps(state))
 subprocess.run(['systemctl','stop','cnc-processor'],check=True)
 with sqlite3.connect('/opt/cnc-processor/processor.db') as db,sqlite3.connect(backup/'processor.db') as target:db.backup(target)
 (backup/'processor.db').chmod(0o600)
 print(json.dumps({'prepared':True,'rollback':str(backup)}))
except Exception:
 try:api('processing-state',{'enabled':initial})
 except Exception:pass
 raise
'''

ACTIVATE = '''
state=json.loads((release/'rollback/state.json').read_text())
for name,path in [('cnc-processor','/opt/cnc-processor/cnc-processor'),('cnc-worker','/opt/cnc/cnc-worker-linux')]:
 destination=Path(path);temporary=destination.with_name(destination.name+'.manifest-new');shutil.copy2(release/name,temporary);temporary.chmod(0o700);temporary.replace(destination)
dropin.parent.mkdir(parents=True,exist_ok=True)
dropin.write_text('[Service]\\nEnvironment=CNC_PROCESSOR_PINNED_PROGRAMS='+','.join(state['pinned'])+'\\n')
subprocess.run(['systemctl','daemon-reload'],check=True)
subprocess.run(['systemctl','start','cnc-processor'],check=True)
'''

FINISH = '''
state=json.loads((release/'rollback/state.json').read_text());deadline=time.monotonic()+90
while True:
 try:
  health=api('health');w=workers()
  if len(w)==6 and all(x['status']=='online' and x['current_load']==0 for x in w):break
 except OSError:pass
 if time.monotonic()>deadline:raise RuntimeError('Core services did not recover')
 time.sleep(2)
with sqlite3.connect('/opt/cnc-processor/processor.db') as db:
 assert dict(db.execute('SELECT program_id,version FROM processor_programs'))==state['versions']
 assert db.execute('PRAGMA quick_check').fetchone()[0]=='ok'
api('processing-state',{'enabled':state['enabled']})
print(json.dumps({'workers_online':6,'private_versions_preserved':True,'rollback':str(release/'rollback'),'processing_restored':state['enabled']}))
'''

ROLLBACK = '''
backup=release/'rollback';state=json.loads((backup/'state.json').read_text())
subprocess.run(['systemctl','stop','cnc-processor'],check=True)
for name,path in [('cnc-processor','/opt/cnc-processor/cnc-processor'),('cnc-worker-linux','/opt/cnc/cnc-worker-linux')]:shutil.copy2(backup/name,path)
if (backup/'manifest-pins.conf').exists():shutil.copy2(backup/'manifest-pins.conf',dropin)
else:dropin.unlink(missing_ok=True)
subprocess.run(['systemctl','daemon-reload'],check=True);subprocess.run(['systemctl','start','cnc-processor'],check=True)
for i in range(30):
 try:api('processing-state',{'enabled':state['enabled']});break
 except OSError:time.sleep(1)
'''


def main():
    artifacts = {'cnc-processor': Path('/tmp/cnc-manifest-processor'), 'cnc-worker': Path('/tmp/cnc-manifest-worker')}
    hashes = {name: hashlib.sha256(path.read_bytes()).hexdigest() for name,path in artifacts.items()}
    tag = 'folder-manifests-'+hashlib.sha256(json.dumps(hashes,sort_keys=True).encode()).hexdigest()[:16]
    release = '/opt/cnc/queue-releases/'+tag
    stage = ROOT/'scratch'/tag;stage.mkdir(parents=True,exist_ok=True)
    archive=stage/'core-source.tar.gz'
    # Explicit unrestricted roots only; never traverse processor/tools or Kiro.
    roots=['processor/api','processor/cluster','processor/internal','processor/cmd','processor/examples/folder-manifest']
    paths=[]
    for name in roots:
        for folder,dirs,files in os.walk(ROOT/name):
            dirs[:]=[d for d in dirs if d not in {'tools','.kiro','__pycache__'}]
            paths += [Path(folder)/file for file in files]
    paths += [ROOT/name for name in ['processor/go.mod','processor/go.sum','processor/CONFIGURATION.md','processor/QUEUE_ARCHITECTURE.md','processor/config.simple.example.json','infra/linode/rollout-manifests.py']]
    with tarfile.open(archive,'w:gz') as tar:
        for p in paths:tar.add(p,arcname=str(p.relative_to(ROOT)))
    ssh('cnc-linode-server','mkdir -p '+shlex.quote(release))
    for name,path in artifacts.items():upload('cnc-linode-server',path,release+'/'+name)
    upload('cnc-linode-server',archive,release+'/core-source.tar.gz')
    ssh('cnc-linode-server','mkdir -p '+shlex.quote(release+'/source')+' && tar -xzf '+shlex.quote(release+'/core-source.tar.gz')+' -C '+shlex.quote(release+'/source'))
    def remote(code):return ssh('cnc-linode-server','python3 -','RELEASE='+repr(release)+'\n'+COMMON+code)
    print(remote(PREPARE),flush=True)
    changed=[]
    try:
        for host in WORKERS:
            upload(host,artifacts['cnc-worker'],'/opt/cnc/cnc-worker.manifest-new')
            backup='/opt/cnc/queue-rollbacks/'+tag
            ssh(host,'set -eu\nmkdir -p '+shlex.quote(backup)+'\ncp -p /opt/cnc/cnc-worker '+shlex.quote(backup+'/cnc-worker')+'\nsystemctl stop cnc-worker\nchmod 700 /opt/cnc/cnc-worker.manifest-new\nmv /opt/cnc/cnc-worker.manifest-new /opt/cnc/cnc-worker')
            changed.append(host)
        remote(ACTIVATE)
        for host in WORKERS:ssh(host,'systemctl start cnc-worker')
        result=json.loads(remote(FINISH));result.update(release=release,sha256=hashes,private_tools_rebuilt=False)
        (stage/'rollout.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result))
    except Exception:
        for host in changed:
            ssh(host,'set -eu\nsystemctl stop cnc-worker\ncp -p '+shlex.quote('/opt/cnc/queue-rollbacks/'+tag+'/cnc-worker')+' /opt/cnc/cnc-worker\nsystemctl start cnc-worker')
        remote(ROLLBACK)
        raise


if __name__=='__main__':main()

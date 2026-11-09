#!/usr/bin/env python3
"""Deploy the 70% admission threshold with bounded memory bursting using explicit core artifacts only."""
import concurrent.futures
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[2]
WORKERS = [f'cnc-linode-worker-{i}' for i in range(1, 7)]
ARTIFACTS = {name: Path('/tmp/cnc-memory-burst-'+name) for name in ['worker', 'server', 'processor']}


def ssh(host, code):
    return subprocess.check_output(['ssh', '-F', str(ROOT/'.linode/ssh_config'), '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=15', host, 'python3', '-'], input=code.encode()).decode()


def upload(host, source, target):
    subprocess.run(['scp', '-q', '-F', str(ROOT/'.linode/ssh_config'), str(source), host+':'+target], check=True)


COMMON = '''
import pathlib,json,urllib.request,shutil,subprocess,hashlib,time,sqlite3
env={}
for line in pathlib.Path('/opt/cnc/processor.env').read_text().splitlines():
 k,_,v=line.partition('=');env[k]=v.strip('"')
def api(path,body=None):
 r=urllib.request.Request('http://127.0.0.1:9191/v1/'+path,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':'Bearer '+env['CNC_PROCESSOR_TOKEN'],'Content-Type':'application/json'},method='GET' if body is None else 'PUT')
 return json.load(urllib.request.urlopen(r,timeout=15))
def workers():
 r=urllib.request.Request(env['CNC_PROCESSOR_BROKER_URL'].rstrip('/')+'/api/processor/cluster/workers',headers={'Authorization':'Bearer '+env['CNC_PROCESSOR_TOKEN']})
 return json.load(urllib.request.urlopen(r,timeout=15))
def replace(source,target):
 target=pathlib.Path(target);temp=target.with_name(target.name+'.memory-burst-new')
 shutil.copy2(source,temp);temp.chmod(0o700);temp.replace(target)
'''


def main():
    hashes = {name: hashlib.sha256(p.read_bytes()).hexdigest() for name,p in ARTIFACTS.items()}
    release = '/opt/cnc/queue-releases/memory-burst-'+hashes['worker'][:16]
    paths = json.loads(Path('/tmp/cnc-memory-burst-files.json').read_text()) + ['infra/linode/rollout-memory-burst.py', 'infra/linode/verify-memory-burst.py']
    archive = Path('/tmp/cnc-memory-burst-source.tar.gz')
    with tarfile.open(archive,'w:gz') as tar:
        for name in paths:
            tar.add(ROOT/name,arcname=name,recursive=False)
    metadata = {'release':release,'sha256':hashes,'base_release':'/opt/cnc/queue-releases/memory-budget-6d3025e5c457b3d0','files':paths}
    Path('/tmp/cnc-memory-burst-release.json').write_text(json.dumps(metadata,indent=2)+'\n')
    ssh('cnc-linode-server', 'import pathlib\npathlib.Path('+repr(release)+').mkdir(parents=True,exist_ok=True)\n')
    for name, path in ARTIFACTS.items():
        upload('cnc-linode-server',path,release+'/cnc-'+name)
    upload('cnc-linode-server',archive,release+'/source-patch.tar.gz')
    upload('cnc-linode-server','/tmp/cnc-memory-burst-release.json',release+'/release.json')
    ssh('cnc-linode-server', 'import pathlib,tarfile\nr=pathlib.Path('+repr(release)+')\nwith tarfile.open(r/"source-patch.tar.gz") as t:t.extractall(r/"source",filter="data")\n')
    with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
        list(pool.map(lambda host:upload(host,ARTIFACTS['worker'],'/tmp/cnc-memory-burst-worker'),WORKERS))
    prefix = 'RELEASE='+repr(release)+'\nHASHES='+repr(hashes)+'\n'
    initial = json.loads(ssh('cnc-linode-server',prefix+COMMON+'''
h=api('health');w=workers()
assert len(w)==6 and all(x['status']=='online' and x['current_load']==0 for x in w),'Workers busy; no service changes made'
assert h['counts']['running']==0,'Processor busy; no service changes made'
r=pathlib.Path(RELEASE);backup=r/'rollback';backup.mkdir(exist_ok=True)
for name,target in [('server','/opt/cnc/cnc-server'),('processor','/opt/cnc-processor/cnc-processor'),('worker-linux','/opt/cnc/cnc-worker-linux')]:
 assert not (backup/('cnc-'+name)).exists(),'Release already deployed; inspect before repeating'
 shutil.copy2(target,backup/('cnc-'+name))
with sqlite3.connect('/opt/cnc-processor/processor.db') as src,sqlite3.connect(backup/'processor.db') as dst:src.backup(dst)
(backup/'processor.db').chmod(0o600)
for name in ['server','processor','worker']:
 assert hashlib.sha256((r/('cnc-'+name)).read_bytes()).hexdigest()==HASHES[name]
api('processing-state',{'enabled':False})
print(json.dumps({'enabled':h['enabled']}))
'''))
    deployed = []
    try:
        ssh('cnc-linode-server',prefix+COMMON+'''
subprocess.run(['systemctl','stop','cnc-processor'],check=True)
subprocess.run(['systemctl','stop','cnc-server'],check=True)
replace(pathlib.Path(RELEASE)/'cnc-server','/opt/cnc/cnc-server')
replace(pathlib.Path(RELEASE)/'cnc-processor','/opt/cnc-processor/cnc-processor')
subprocess.run(['systemctl','start','cnc-server'],check=True)
subprocess.run(['systemctl','start','cnc-processor'],check=True)
for _ in range(45):
 w=workers()
 if len(w)==6 and all(x['status']=='online' and x['current_load']==0 for x in w):break
 time.sleep(1)
else:raise RuntimeError('Old workers did not reconnect before rollout')
''')
        for host in WORKERS:
            ready = json.loads(ssh('cnc-linode-server',COMMON+f"\nw=next(x for x in workers() if x['id']=={host!r})\nprint(json.dumps(w['status']=='online' and w['current_load']==0))\n"))
            if not ready:
                raise RuntimeError(host+' became busy; no restart allowed')
            print(ssh(host,prefix+COMMON.split('env={}')[0]+'''
r=pathlib.Path(RELEASE);backup=r/'rollback';backup.mkdir(parents=True,exist_ok=True)
target=pathlib.Path('/opt/cnc/cnc-worker');new=pathlib.Path('/tmp/cnc-memory-burst-worker')
assert hashlib.sha256(new.read_bytes()).hexdigest()==HASHES['worker']
old=backup/'cnc-worker';assert not old.exists(),'Worker already updated; inspect before repeating'
shutil.copy2(target,old)
temp=target.with_name('cnc-worker.memory-burst-new');shutil.copy2(new,temp);temp.chmod(0o700);temp.replace(target)
try:
 subprocess.run(['systemctl','restart','cnc-worker'],check=True)
 subprocess.run(['systemctl','is-active','--quiet','cnc-worker'],check=True)
except Exception:
 shutil.copy2(old,target);subprocess.run(['systemctl','restart','cnc-worker'],check=False);raise
print(json.dumps({'installed':True,'worker_sha256':hashlib.sha256(target.read_bytes()).hexdigest()}))
''').strip(),flush=True)
            deployed.append(host)
        # Workers retry connection automatically after the controller restart.
        ssh('cnc-linode-server',prefix+COMMON+'''
for _ in range(45):
 w=workers()
 if len(w)==6 and all(x['status']=='online' and x['current_load']==0 for x in w):break
 time.sleep(1)
else:raise RuntimeError('Workers failed to reconnect')
replace(pathlib.Path(RELEASE)/'cnc-worker','/opt/cnc/cnc-worker-linux')
''')
    except Exception:
        for host in deployed:
            ssh(host,'RELEASE='+repr(release)+'\n'+COMMON.split('env={}')[0]+'''\nshutil.copy2(pathlib.Path(RELEASE)/'rollback/cnc-worker','/opt/cnc/cnc-worker')\nsubprocess.run(['systemctl','restart','cnc-worker'],check=True)\n''')
        ssh('cnc-linode-server',prefix+COMMON+'''
subprocess.run(['systemctl','stop','cnc-processor'],check=False)
subprocess.run(['systemctl','stop','cnc-server'],check=False)
backup=pathlib.Path(RELEASE)/'rollback'
for name,target in [('server','/opt/cnc/cnc-server'),('processor','/opt/cnc-processor/cnc-processor'),('worker-linux','/opt/cnc/cnc-worker-linux')]:replace(backup/('cnc-'+name),target)
subprocess.run(['systemctl','start','cnc-server'],check=True)
subprocess.run(['systemctl','start','cnc-processor'],check=True)
''')
        raise
    finally:
        print(ssh('cnc-linode-server',COMMON+f"\napi('processing-state',{{'enabled':{initial['enabled']!r}}})\nprint(json.dumps({{'processing_restored':{initial['enabled']!r},'workers_updated':{deployed!r}}}))\n").strip(),flush=True)
    print(json.dumps({'release':release,'sha256':hashes,'admission_memory_percent':70,'emergency_memory_percent':85,'minimum_worker_memory_mb':512}),flush=True)


if __name__ == '__main__':
    main()

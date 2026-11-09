#!/usr/bin/env python3
"""Deploy automatic publication binaries only; never read private tool files."""
import hashlib
import json
from pathlib import Path
import shlex
import subprocess

ROOT = Path(__file__).resolve().parents[2]
ARTIFACTS = Path('/tmp/cnc-auto-publication-release')


def ssh(host, script, data=None):
    return subprocess.check_output(['ssh', '-F', str(ROOT/'.linode/ssh_config'),
        '-o', 'ConnectTimeout=10', host, script], input=data).decode()


def upload(host, source, destination):
    subprocess.run(['scp', '-F', str(ROOT/'.linode/ssh_config'), str(source),
                    host+':'+destination], check=True)


PREPARE = r'''
import json,sqlite3,subprocess,time,urllib.request,shutil
from pathlib import Path
release=Path(RELEASE)
settings=dict(line.split('=',1) for line in Path('/opt/cnc/processor.env').read_text().splitlines() if '=' in line and not line.startswith('#'))
headers={'Authorization':'Bearer '+settings['CNC_PROCESSOR_TOKEN'].strip('"'),'Content-Type':'application/json'}
def api(path,body=None):
 r=urllib.request.Request('http://127.0.0.1:9191/v1/'+path,data=None if body is None else json.dumps(body).encode(),headers=headers,method='GET' if body is None else 'PUT')
 with urllib.request.urlopen(r,timeout=15) as response:return json.load(response)
initial=api('health')['enabled']
api('processing-state',{'enabled':False})
deadline=time.monotonic()+180
while api('health')['counts']['running']:
 if time.monotonic()>deadline:raise RuntimeError('Active jobs have not drained; no binaries replaced')
 time.sleep(2)
cluster=api('cluster')
if len(cluster['workers'])!=6 or any(w['current_load'] for w in cluster['workers']):
 api('processing-state',{'enabled':initial})
 raise RuntimeError('Expected six idle workers; no binaries replaced')
backup=release/'rollback';backup.mkdir()
for name,source in [('cnc-processor','/opt/cnc-processor/cnc-processor'),('cnc-worker-linux','/opt/cnc/cnc-worker-linux'),('config.json','/opt/cnc-processor/config.json')]:shutil.copy2(source,backup/name)
with sqlite3.connect('/opt/cnc-processor/processor.db') as db,sqlite3.connect(backup/'processor.db') as dest:db.backup(dest)
(backup/'state.json').write_text(json.dumps({'enabled':initial}))
subprocess.run(['systemctl','stop','cnc-processor'],check=True)
print(json.dumps({'prepared':True,'previous_enabled':initial}))
'''


ACTIVATE = r'''
import shutil,subprocess
from pathlib import Path
release=Path(RELEASE)
for name,destination in [('cnc-processor','/opt/cnc-processor/cnc-processor'),('cnc-worker','/opt/cnc/cnc-worker-linux')]:
 target=Path(destination);temporary=target.with_suffix('.auto-new')
 shutil.copy2(release/name,temporary);temporary.chmod(0o700);temporary.replace(target)
subprocess.run(['systemctl','start','cnc-processor'],check=True)
print('Automatic publication controller activated; intake paused')
'''


def remote(source, directory):
    return ssh('cnc-linode-server','python3 -',('RELEASE='+repr(directory)+'\n'+source).encode())


def main():
    hashes={name:hashlib.sha256((ARTIFACTS/name).read_bytes()).hexdigest() for name in ('cnc-processor','cnc-worker')}
    release='auto-publish-'+hashlib.sha256(json.dumps(hashes,sort_keys=True).encode()).hexdigest()[:16]
    directory='/opt/cnc/queue-releases/'+release
    nodes=json.loads((ROOT/'.linode/state.json').read_text())['servers']
    workers=sorted(name for name in nodes if '-worker-' in name)
    if len(workers)!=6:raise RuntimeError('Expected six workers')
    ssh('cnc-linode-server','mkdir -p '+shlex.quote(directory)+'; chmod 700 '+shlex.quote(directory))
    for name in hashes:upload('cnc-linode-server',ARTIFACTS/name,directory+'/'+name)
    manifest={'release':release,'sha256':hashes,'workers':workers,'rollback':directory+'/rollback'}
    (ARTIFACTS/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    upload('cnc-linode-server',ARTIFACTS/'manifest.json',directory+'/manifest.json')
    for worker in workers:
        upload(worker,ARTIFACTS/'cnc-worker','/opt/cnc/cnc-worker.auto-new')
    print(remote(PREPARE,directory),flush=True)
    changed=[]
    try:
        for worker in workers:
            backup='/opt/cnc/queue-rollbacks/'+release
            ssh(worker,'set -eu\nmkdir -p '+shlex.quote(backup)+'\ncp -p /opt/cnc/cnc-worker '+shlex.quote(backup+'/cnc-worker')+'\nsystemctl stop cnc-worker\nchmod 700 /opt/cnc/cnc-worker.auto-new\nmv /opt/cnc/cnc-worker.auto-new /opt/cnc/cnc-worker')
            changed.append(worker)
        print(remote(ACTIVATE,directory),flush=True)
        for worker in workers:ssh(worker,'systemctl start cnc-worker')
    except Exception:
        for worker in changed:
            ssh(worker,'set -eu\nsystemctl stop cnc-worker\ncp -p '+shlex.quote('/opt/cnc/queue-rollbacks/'+release+'/cnc-worker')+' /opt/cnc/cnc-worker\nsystemctl start cnc-worker')
        print(remote("import shutil,subprocess\nsubprocess.run(['systemctl','stop','cnc-processor'],check=True)\nshutil.copy2(RELEASE+'/rollback/cnc-processor','/opt/cnc-processor/cnc-processor')\nshutil.copy2(RELEASE+'/rollback/cnc-worker-linux','/opt/cnc/cnc-worker-linux')\nsubprocess.run(['systemctl','start','cnc-processor'],check=True)\n",directory),flush=True)
        raise
    manifest['state']='deployed-paused'
    print(json.dumps(manifest),flush=True)


if __name__=='__main__':main()

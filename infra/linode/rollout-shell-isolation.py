#!/usr/bin/env python3
"""Roll out the common shell-runner fix; never read or ship private tools."""
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
META = json.loads(Path('/tmp/cnc-shell-isolation-release.json').read_text())
RELEASE = META['release']
WORKERS = [f'cnc-linode-worker-{i}' for i in range(1, 7)]


def ssh(host, code):
    return subprocess.check_output(
        ['ssh', '-F', str(ROOT/'.linode/ssh_config'), '-o', 'BatchMode=yes', host, 'python3', '-'],
        input=code.encode(), text=False).decode()


def upload(host, source, target):
    subprocess.run(['scp', '-F', str(ROOT/'.linode/ssh_config'), str(source), host+':'+target], check=True)


COMMON = '''
import pathlib,json,urllib.request,shutil,subprocess,hashlib,time,os
env={}
for line in pathlib.Path('/opt/cnc/processor.env').read_text().splitlines():
 k,_,v=line.partition('=');env[k]=v.strip('"')
def api(path,body=None):
 r=urllib.request.Request('http://127.0.0.1:9191/v1/'+path,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':'Bearer '+env['CNC_PROCESSOR_TOKEN'],'Content-Type':'application/json'},method='GET' if body is None else 'PUT')
 return json.load(urllib.request.urlopen(r,timeout=15))
def workers():
 r=urllib.request.Request(env['CNC_PROCESSOR_BROKER_URL'].rstrip('/')+'/api/processor/cluster/workers',headers={'Authorization':'Bearer '+env['CNC_PROCESSOR_TOKEN']})
 return json.load(urllib.request.urlopen(r,timeout=15))
'''


def main():
    binary = Path('/tmp/cnc-shell-isolation-worker')
    assert hashlib.sha256(binary.read_bytes()).hexdigest() == META['sha256']
    ssh('cnc-linode-server', 'import pathlib\npathlib.Path('+repr(RELEASE)+').mkdir(parents=True,exist_ok=True)\n')
    upload('cnc-linode-server', binary, RELEASE+'/cnc-worker')
    upload('cnc-linode-server', '/tmp/cnc-shell-isolation-source.tar.gz', RELEASE+'/core-source.tar.gz')
    ssh('cnc-linode-server', 'import pathlib,tarfile\nr=pathlib.Path('+repr(RELEASE)+')\nwith tarfile.open(r/"core-source.tar.gz") as t:t.extractall(r/"source",filter="data")\n')
    for host in WORKERS:
        upload(host, binary, '/tmp/cnc-shell-isolation-worker')
    initial = json.loads(ssh('cnc-linode-server', COMMON+'''
state=api('health');w=workers()
assert len(w)==6 and all(x['status']=='online' and x['current_load']==0 for x in w),'Workers busy; rollout not started'
assert state['counts']['running']==0,'Processor busy; rollout not started'
api('processing-state',{'enabled':False})
print(json.dumps({'enabled':state['enabled']}))
'''))
    deployed = []
    try:
        for host in WORKERS:
            ready = json.loads(ssh('cnc-linode-server', COMMON+f"\nw=next(x for x in workers() if x['id']=={host!r})\nprint(json.dumps(w['current_load']==0 and w['status']=='online'))\n"))
            if not ready:
                raise RuntimeError(host+' became busy; do not restart it')
            print(ssh(host, f"RELEASE={RELEASE!r}\nEXPECTED={META['sha256']!r}\n"+'''
import pathlib,shutil,hashlib,subprocess,json
r=pathlib.Path(RELEASE);backup=r/'rollback';backup.mkdir(parents=True,exist_ok=True)
target=pathlib.Path('/opt/cnc/cnc-worker');new=pathlib.Path('/tmp/cnc-shell-isolation-worker')
assert hashlib.sha256(new.read_bytes()).hexdigest()==EXPECTED
old=backup/'cnc-worker'
if not old.exists():shutil.copy2(target,old)
temp=target.with_name('cnc-worker.shell-isolation-new');shutil.copy2(new,temp);temp.chmod(0o700);temp.replace(target)
try:
 subprocess.run(['systemctl','restart','cnc-worker'],check=True)
 subprocess.run(['systemctl','is-active','--quiet','cnc-worker'],check=True)
except Exception:
 shutil.copy2(old,target);subprocess.run(['systemctl','restart','cnc-worker'],check=False);raise
print(json.dumps({'installed':True,'rollback':str(old),'sha256':hashlib.sha256(target.read_bytes()).hexdigest()}))
''').strip(), flush=True)
            deployed.append(host)
            # Reconnect before proceeding; processor scheduling stays paused.
            for _ in range(30):
                online = json.loads(ssh('cnc-linode-server', COMMON+f"\nprint(json.dumps(any(x['id']=={host!r} and x['status']=='online' for x in workers())))\n"))
                if online:
                    break
                import time
                time.sleep(1)
            else:
                raise RuntimeError(host+' did not reconnect')
        print(ssh('cnc-linode-server', f"RELEASE={RELEASE!r}\n"+COMMON+'''
r=pathlib.Path(RELEASE);backup=r/'rollback';backup.mkdir(exist_ok=True)
target=pathlib.Path('/opt/cnc/cnc-worker-linux');old=backup/'cnc-worker-linux'
if not old.exists():shutil.copy2(target,old)
new=target.with_name('cnc-worker-linux.shell-isolation-new');shutil.copy2(r/'cnc-worker',new);new.chmod(0o700);new.replace(target)
w=workers();assert len(w)==6 and all(x['status']=='online' for x in w)
print(json.dumps({'workers_online':6,'download_binary_updated':True,'release':str(r)}))
''').strip(), flush=True)
    finally:
        print(ssh('cnc-linode-server', COMMON+f"\napi('processing-state',{{'enabled':{initial['enabled']!r}}})\nprint(json.dumps({{'processing_restored':{initial['enabled']!r},'updated_workers':{deployed!r}}}))\n").strip(), flush=True)


if __name__ == '__main__':
    main()

#!/usr/bin/env python3
"""Publish a worker artifact and roll it out with durable /root inputs preserved."""
import argparse
import concurrent.futures
import hashlib
import json
import re
import shlex
import subprocess
from pathlib import Path

ID=re.compile(r'[A-Za-z0-9][A-Za-z0-9_.-]{0,127}')


def ssh(config,alias,code,payload):
    result=subprocess.run(['ssh','-F',str(config),'-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','ConnectTimeout=15',alias,'python3 -c '+shlex.quote(code)],input=json.dumps(payload),text=True,capture_output=True)
    if result.returncode:raise RuntimeError(alias+': '+result.stderr[-500:])
    return json.loads(result.stdout)


PUBLISH=r'''
import json,sys,hashlib,shutil,os,time
from pathlib import Path
p=json.load(sys.stdin);source=Path(p['stage']);target=Path('/opt/cnc/cnc-worker-linux')
assert not source.is_symlink() and hashlib.sha256(source.read_bytes()).hexdigest()==p['sha256']
backup=Path('/opt/cnc-deploy')/('worker-artifact-before-'+str(time.time_ns()))
if target.exists():shutil.copy2(target,backup)
source.chmod(0o700);os.replace(source,target)
print(json.dumps({'artifact_sha256':hashlib.sha256(target.read_bytes()).hexdigest(),'rollback':str(backup)}))
'''

ROLL=r'''
import json,sys,hashlib,shutil,subprocess,tempfile,os,time,urllib.request
from pathlib import Path
p=json.load(sys.stdin);root=Path('/opt/cnc');stage=Path('/opt/cnc-deploy');stamp=str(time.time_ns())
new=stage/('cnc-worker-new-'+p['sha256'][:12]);cfg=root/'config.json'
assert not new.is_symlink() and not cfg.is_symlink()
op=urllib.request.build_opener(urllib.request.ProxyHandler({}))
with op.open(p['url'],timeout=60) as response:data=response.read((64<<20)+1)
assert len(data)<=64<<20 and hashlib.sha256(data).hexdigest()==p['sha256']
assert b'retain spread input' in data
new.write_bytes(data);new.chmod(0o700)
backup=stage/('worker-release-before-'+stamp);backup.mkdir(mode=0o700)
for name in ('cnc-worker','worker-stop-guard','config.json'):
 if (root/name).exists():shutil.copy2(root/name,backup/name)
# systemd's existing drain guard waits for active work and delivery to finish.
subprocess.run(['systemctl','stop','cnc-worker'],check=True)
try:
 value=json.loads(cfg.read_text());value['spread_workspace_root']='/root'
 fd,temp=tempfile.mkstemp(dir=root,prefix='.worker-config-')
 with os.fdopen(fd,'w') as output:json.dump(value,output,indent=2)
 os.replace(temp,cfg)
 os.replace(new,root/'cnc-worker');shutil.copy2(root/'cnc-worker',root/'worker-stop-guard')
 subprocess.run(['systemctl','start','cnc-worker'],check=True)
 for _ in range(60):
  pid=subprocess.check_output(['systemctl','show','cnc-worker','--property=MainPID','--value'],text=True).strip()
  try:running=hashlib.sha256(Path('/proc',pid,'exe').read_bytes()).hexdigest()
  except OSError:running=None
  if running==p['sha256']:break
  time.sleep(.25)
 else:raise RuntimeError('Running worker does not match the published artifact')
except BaseException:
 subprocess.run(['systemctl','stop','cnc-worker'],check=False)
 for name in ('cnc-worker','worker-stop-guard','config.json'):
  if (backup/name).exists():shutil.copy2(backup/name,root/name)
 subprocess.run(['systemctl','start','cnc-worker'],check=False);raise
print(json.dumps({'running_sha256':running,'spread_workspace_root':value['spread_workspace_root'],'service':'active','rollback':str(backup)}))
'''

RESTORE_PLAN=r'''
import json,sys,urllib.request,hashlib
from pathlib import Path
p=json.load(sys.stdin);cfg=json.loads(Path('/opt/cnc/config.json').read_text());base='http://'+cfg['http_addr'];op=urllib.request.build_opener(urllib.request.ProxyHandler({}))
with op.open(base+'/api/jobs/'+p['job']+'/tasks',timeout=30) as response:tasks=json.load(response)
plan={}
for task in tasks:
 payload=task.get('payload') or {};name=payload.get('dest_name');path=Path(payload.get('chunk_path') or '')
 if not name or Path(name).name!=name or not path.is_absolute() or path.is_symlink():raise RuntimeError('Invalid original chunk')
 if not str(path).startswith('/root/split/'):raise RuntimeError('Unexpected chunk source')
 data=path.read_bytes()
 plan[task['assigned_to']]={'name':name,'url':base+payload['download_url'],'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()}
print(json.dumps(plan))
'''

RESTORE=r'''
import json,sys,urllib.request,hashlib,tempfile,os,shutil,time
from pathlib import Path
p=json.load(sys.stdin);target=Path('/root')/p['name']
assert target.parent==Path('/root') and not target.is_symlink()
op=urllib.request.build_opener(urllib.request.ProxyHandler({}))
with op.open(p['url'],timeout=60) as response:data=response.read(p['bytes']+1)
assert len(data)==p['bytes'] and hashlib.sha256(data).hexdigest()==p['sha256']
if target.exists() and hashlib.sha256(target.read_bytes()).hexdigest()!=p['sha256']:shutil.copy2(target,target.with_name(target.name+'.before-restore-'+str(time.time_ns())))
fd,temp=tempfile.mkstemp(dir=target.parent,prefix='.received-input-')
with os.fdopen(fd,'wb') as output:output.write(data);output.flush();os.fsync(output.fileno())
os.replace(temp,target)
print(json.dumps({'retained_path':str(target),'bytes':target.stat().st_size,'sha256':hashlib.sha256(target.read_bytes()).hexdigest()}))
'''


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--ssh-config',type=Path,required=True);parser.add_argument('--state',type=Path,required=True)
    parser.add_argument('--binary',type=Path,required=True);parser.add_argument('--parallel',type=int,default=2)
    parser.add_argument('--restore-job')
    args=parser.parse_args();state=json.loads(args.state.read_text());nodes=state['nodes']
    controller=nodes['controller']['name'];workers=[n['name'] for role,n in nodes.items() if role.startswith('worker-')]
    if not workers or not all(ID.fullmatch(n) for n in [controller,*workers]) or not 1<=args.parallel<=2:raise ValueError('Invalid deployment inventory')
    data=args.binary.read_bytes()
    if data[:4]!=b'\x7fELF' or b'retain spread input' not in data:raise ValueError('Worker artifact lacks durable input support')
    digest=hashlib.sha256(data).hexdigest();stage='/opt/cnc-deploy/cnc-worker-release-'+digest[:12]
    subprocess.run(['scp','-F',str(args.ssh_config),str(args.binary),controller+':'+stage],check=True)
    print(json.dumps({'controller':ssh(args.ssh_config,controller,PUBLISH,{'stage':stage,'sha256':digest})}),flush=True)
    plan={}
    if args.restore_job:
        if not ID.fullmatch(args.restore_job):raise ValueError('Invalid restore job ID')
        plan=ssh(args.ssh_config,controller,RESTORE_PLAN,{'job':args.restore_job})
        if set(plan)!=set(workers):raise ValueError('Original job does not cover the managed workers')
    url='http://'+nodes['controller']['private_ip']+':8080/download/cnc-worker-linux'
    def update(worker):
        print(json.dumps({'worker':worker,'phase':'draining and updating'}),flush=True)
        result=ssh(args.ssh_config,worker,ROLL,{'url':url,'sha256':digest})
        if worker in plan:result['restored_input']=ssh(args.ssh_config,worker,RESTORE,plan[worker])
        print(json.dumps({'worker':worker,**result}),flush=True)
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.parallel) as pool:
        for _ in pool.map(update,sorted(workers)):pass
    print(json.dumps({'workers_updated':len(workers),'artifact_sha256':digest,'restore_job':args.restore_job}),flush=True)

if __name__=='__main__':main()

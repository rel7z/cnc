#!/usr/bin/env python3
"""Install a checksum-pinned worker agent; never access or run tool programs."""
import concurrent.futures
import json
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
EXPECTED = 'f9407fba684a566494581d8f97e48fb8837f65c09d1879e12fd08a7717bcc617'
REMOTE = r'''
import hashlib,json,os,pathlib,shutil,subprocess,time,urllib.request
P=pathlib.Path
root=P('/opt/cnc')
c=json.loads((root/'config.json').read_text())
runs=P(c['data_dir'])/'shell-runs'
assert not runs.exists() or not any(runs.iterdir()),'Worker busy; no restart performed'
pid=subprocess.check_output(['systemctl','show','-p','MainPID','--value','cnc-worker'],text=True).strip()
target=P('/proc',pid,'exe').resolve()
assert str(target) in ('/opt/cnc/cnc-worker','/opt/cnc/cnc-worker-linux'),'Unexpected worker binary path'
release=root/'worker-diagnostics-releases'/EXPECTED[:16];release.mkdir(parents=True,exist_ok=True,mode=0o700)
stage=release/'cnc-worker'
op=urllib.request.build_opener(urllib.request.ProxyHandler({}))
with op.open('http://172.31.4.149:8080/download/cnc-worker-linux',timeout=60) as r:stage.write_bytes(r.read())
stage.chmod(0o700)
assert hashlib.sha256(stage.read_bytes()).hexdigest()==EXPECTED,'Worker download checksum mismatch'
backup=release/'rollback-worker'
if not backup.exists():shutil.copy2(target,backup)
try:
 tmp=target.with_name(target.name+'.diagnostics-new');shutil.copy2(stage,tmp);os.replace(tmp,target)
 subprocess.run(['systemctl','restart','cnc-worker'],check=True,timeout=30)
 deadline=time.monotonic()+25
 while True:
  state=subprocess.check_output(['systemctl','is-active','cnc-worker'],text=True).strip()
  pid=subprocess.check_output(['systemctl','show','-p','MainPID','--value','cnc-worker'],text=True).strip()
  if state=='active' and pid!='0' and hashlib.sha256(P('/proc',pid,'exe').read_bytes()).hexdigest()==EXPECTED:break
  if time.monotonic()>deadline:raise RuntimeError('Updated worker did not become healthy')
  time.sleep(1)
except BaseException:
 tmp=target.with_name(target.name+'.rollback-new');shutil.copy2(backup,tmp);os.replace(tmp,target)
 subprocess.run(['systemctl','restart','cnc-worker'],check=True,timeout=30)
 raise
print(json.dumps({'agent_sha256':EXPECTED,'worker_service':'active','rollback':str(backup),'tool_files_accessed':False}))
'''.replace('EXPECTED', repr(EXPECTED))

state = json.loads((ROOT/'.aws-cnc/state.json').read_text())
CLI = str(Path.home()/'.local/bin/aws')

def aws(*args):
    result = subprocess.run([CLI,*args,'--profile','cnc','--region',state['region'],'--output','json'],capture_output=True,text=True,check=True)
    return json.loads(result.stdout)

def linode(number):
    name='cnc-linode-worker-'+str(number)
    result=subprocess.run(['ssh','-F',str(ROOT/'.linode/cross-cloud-ssh_config'),name,'python3','-'],input=REMOTE,capture_output=True,text=True,timeout=150)
    if result.returncode:return {'worker':name,'ok':False,'error':result.stderr[-800:]}
    return {'worker':name,'ok':True,**json.loads(result.stdout.strip().splitlines()[-1])}

nodes={name:node['id'] for name,node in state['nodes'].items() if name.startswith('cnc-aws-worker-')}
command="python3 - <<'AGENT'\n"+REMOTE+"\nAGENT\n"
command_id=aws('ssm','send-command','--instance-ids',*nodes.values(),'--document-name','AWS-RunShellScript','--parameters',json.dumps({'commands':[command],'executionTimeout':['180']}))['Command']['CommandId']
print('SSM worker command:',command_id,flush=True)

def aws_worker(item):
    name,node=item
    deadline=time.monotonic()+200
    while True:
        try:result=aws('ssm','get-command-invocation','--command-id',command_id,'--instance-id',node)
        except subprocess.CalledProcessError:
            if time.monotonic()>deadline:raise
            time.sleep(2);continue
        if result['Status'] not in ('Pending','InProgress','Delayed'):break
        if time.monotonic()>deadline:return {'worker':name,'ok':False,'status':'observation timeout','command_id':command_id}
        time.sleep(3)
    if result['Status']!='Success':return {'worker':name,'ok':False,'status':result['Status'],'error':result.get('StandardErrorContent','')[-800:]}
    return {'worker':name,'ok':True,**json.loads(result['StandardOutputContent'].strip().splitlines()[-1])}

with concurrent.futures.ThreadPoolExecutor(max_workers=11) as pool:
    futures=[pool.submit(linode,n) for n in range(1,9)]+[pool.submit(aws_worker,item) for item in nodes.items()]
    results=[future.result() for future in futures]
destination=ROOT/'.aws-cnc/worker-diagnostics-verification.json'
destination.write_text(json.dumps(results,indent=2));destination.chmod(0o600)
print(json.dumps(results,indent=2))
raise SystemExit(0 if all(row['ok'] for row in results) else 1)

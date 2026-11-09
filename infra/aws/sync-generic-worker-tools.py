#!/usr/bin/env python3
"""Repair eight idle Linode tool installations without restarting their agents."""
import base64,concurrent.futures,json,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
check=base64.b64encode((ROOT/'cnc-api/worker_tools_manifest.py').read_bytes()).decode()
script=r'''import base64,json,pathlib,tempfile,tarfile,urllib.request,os,subprocess,time,hashlib
P=pathlib.Path
namespace={'__name__':'readiness'};exec(compile(base64.b64decode('CHECK'),'manifest-check','exec'),namespace)
stage=P(tempfile.mkdtemp(prefix='.tools-stage-',dir='/opt/cnc'))
o=urllib.request.build_opener(urllib.request.ProxyHandler({}))
with o.open('http://172.31.4.149:8080/download/cnc-worker-tools',timeout=120) as r:(stage/'tools.tar.gz').write_bytes(r.read())
with tarfile.open(stage/'tools.tar.gz') as t:t.extractall(stage,filter='data')
count=namespace['verify'](stage/'tools')
node=P('/opt/cnc/node-path').read_text().strip()+'/bin/node'
r=subprocess.run([node,str(stage/'tools/dork2-src/index.js'),'--help'],capture_output=True,text=True,timeout=15)
assert r.returncode==0 and 'Usage: dork2' in r.stderr,'Staged CLI help failed'
config=json.loads(P('/opt/cnc/config.json').read_text());runs=P(config['data_dir'])/'shell-runs'
assert not runs.exists() or not any(runs.iterdir()),'Worker busy; staged package retained for safe retry'
root=P('/opt/cnc/tools');backup=P('/opt/cnc/tools.rollback-'+str(time.time_ns()))
os.rename(root,backup)
try:os.rename(stage/'tools',root)
except BaseException:os.rename(backup,root);raise
results=[]
for attempt in range(2):
 with tempfile.TemporaryDirectory(prefix='cnc-cli-check-') as workspace:
  P(workspace,'tools').symlink_to(root,target_is_directory=True)
  env=dict(os.environ,HOME=workspace,PATH=str(P(node).parent)+':'+os.environ['PATH']);env.pop('APIFY_TOKEN',None)
  r=subprocess.run(['./tools/dork2','--help'],cwd=workspace,env=env,capture_output=True,text=True,timeout=15)
  assert r.returncode==0 and 'Usage: dork2' in r.stderr,'Installed CLI help failed'
  results.append(r.returncode)
print(json.dumps({'files_verified':count,'local_cli_help_exit_codes':results,'worker_restarted':False,'rollback':str(backup),'archive_sha256':hashlib.sha256((stage/'tools.tar.gz').read_bytes()).hexdigest()}))'''.replace('CHECK',check)
def sync(i):
 name='cnc-linode-worker-'+str(i);r=subprocess.run(['ssh','-F',str(ROOT/'.linode/cross-cloud-ssh_config'),name,'python3','-'],input=script,text=True,capture_output=True,timeout=180)
 if r.returncode:return {'worker':name,'ok':False,'error':r.stderr[-600:]}
 return {'worker':name,'ok':True,**json.loads(r.stdout.strip().splitlines()[-1])}
with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:results=list(pool.map(sync,range(1,9)))
p=ROOT/'.linode/generic-tools-verification.json';p.write_text(json.dumps(results,indent=2));p.chmod(0o600)
print(json.dumps(results,indent=2));raise SystemExit(0 if all(r['ok'] for r in results) else 1)

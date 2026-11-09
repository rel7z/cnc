#!/usr/bin/env python3
"""Read-only worker service and network verification; never run tool programs."""
import concurrent.futures,json,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
script=r'''import json,pathlib,os,subprocess,time,urllib.request,shutil
P=pathlib.Path
active=subprocess.run(['systemctl','is-active','cnc-worker'],capture_output=True,text=True).stdout.strip()
wg=subprocess.run(['systemctl','is-active','wg-quick@cncln0'],capture_output=True,text=True).stdout.strip()
handshake=subprocess.run(['wg','show','cncln0','latest-handshakes'],capture_output=True,text=True) if shutil.which('wg') else None
stamps=[int(line.split()[-1]) for line in handshake.stdout.splitlines() if line.split()] if handshake else []
config={}
if P('/opt/cnc/config.json').exists():
 c=json.loads(P('/opt/cnc/config.json').read_text());config={k:c.get(k) for k in ('server_addr','worker_id','max_tasks')}
release_ok=False
if wg=='active':
 try:
  o=urllib.request.build_opener(urllib.request.ProxyHandler({}))
  with o.open('http://172.31.4.149:8080/download/cnc-release',timeout=10) as r:release_ok=r.status==200 and bool(r.read(256))
 except OSError:pass
memory=int(next(line.split()[1] for line in P('/proc/meminfo').read_text().splitlines() if line.startswith('MemTotal:')))//1024
print(json.dumps({'worker_service':active,'wireguard_service':wg,'handshake_recent':bool(stamps) and max(stamps)>time.time()-180,'download_reachable':release_ok,'cpu_count':os.cpu_count(),'memory_mb':memory,'config':config}))'''
def verify(number):
 name='cnc-linode-worker-'+str(number)
 r=subprocess.run(['ssh','-F',str(ROOT/'.linode/cross-cloud-ssh_config'),name,'python3','-'],input=script,text=True,capture_output=True,timeout=40)
 if r.returncode:return {'worker':name,'ssh_ready':False,'exit_code':r.returncode}
 try:result=json.loads(r.stdout.strip().splitlines()[-1])
 except (ValueError,IndexError):return {'worker':name,'ssh_ready':True,'metadata_unavailable':True}
 return dict(worker=name,ssh_ready=True,**result)
with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:results=list(pool.map(verify,range(1,9)))
p=ROOT/'.linode/cross-cloud-verification.json';p.write_text(json.dumps(results,indent=2));p.chmod(0o600)
print(json.dumps(results,indent=2))
raise SystemExit(0 if all(x.get('worker_service')=='active' and x.get('handshake_recent') and x.get('download_reachable') and x.get('cpu_count')==4 and x.get('memory_mb',0)>7500 for x in results) else 1)

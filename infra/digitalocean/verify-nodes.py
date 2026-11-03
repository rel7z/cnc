#!/usr/bin/env python3
"""Read actual hardware/service/binary metadata for exactly the created nodes."""
import concurrent.futures,json,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2];STATE=ROOT/'.digitalocean'
state=json.loads((STATE/'state.json').read_text());manifest=json.loads((STATE/'release/manifest.json').read_text())
script=r'''
import hashlib,json,os,subprocess
from pathlib import Path
role=ROLE;unit='cnc-server' if role=='controller' else 'cnc-worker'
pid=subprocess.check_output(['systemctl','show',unit,'-p','MainPID','--value'],text=True).strip()
memory=int(next(l.split()[1] for l in Path('/proc/meminfo').read_text().splitlines() if l.startswith('MemTotal:')))//1024
result={'role':role,'hostname':subprocess.check_output(['hostname'],text=True).strip(),'cpus':os.cpu_count(),'memory_mib':memory,'service':subprocess.check_output(['systemctl','is-active',unit],text=True).strip(),'running_sha256':hashlib.sha256((Path('/proc')/pid/'exe').read_bytes()).hexdigest()}
if role!='controller':
 result['scanner_sha256']=hashlib.sha256(Path('/opt/cnc/tools/cnc-enum').read_bytes()).hexdigest();result['login_folder_exists']=Path('/root/processor').is_dir()
print(json.dumps(result))
'''
def verify(role):
 node=state['nodes'][role]
 result=subprocess.run(['ssh','-F',str(STATE/'ssh_config'),node['name'],'python3 -'],input='ROLE='+repr(role)+'\n'+script,text=True,capture_output=True,check=True,timeout=30)
 data=json.loads(result.stdout);expected=manifest['sha256']['bin/cnc-server' if role=='controller' else 'bin/cnc-worker']
 assert data['running_sha256']==expected and data['service']=='active' and data['cpus']==4 and data['memory_mib']>=7500
 if role!='controller':assert data['scanner_sha256']==manifest['sha256']['tools/cnc-enum'] and data['login_folder_exists']
 return data
with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:results=list(pool.map(verify,state['nodes']))
(STATE/'node-verification.json').write_text(json.dumps(results,indent=2)+'\n');print(json.dumps(results))

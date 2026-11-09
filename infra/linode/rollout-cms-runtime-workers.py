#!/usr/bin/env python3
"""Prepare/apply a reviewed core+scanner rollout; default is an offline dry run."""
import argparse,hashlib,json,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
REMOTE=r'''
import hashlib,json,shutil,subprocess,time
from pathlib import Path
root=Path('/opt/cnc');folder=root/'rollouts'/release
if json.loads((root/'config.json').read_text())['worker_id']!=worker:raise RuntimeError('Worker identity mismatch')
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def state():
 return dict(line.split('=',1) for line in subprocess.check_output(['systemctl','show','cnc-worker','-p','MainPID','-p','ControlPID','-p','ActiveState','-p','SubState','-p','Job'],text=True).splitlines())
before=state()
if before['ActiveState']!='active' or before['ControlPID']!='0' or before.get('Job','') not in ('','0'):raise RuntimeError('Worker maintenance already pending')
for name in ['cnc-worker','cnc-enum']:
 if sha(folder/(name+'.new'))!=digests[name]:raise RuntimeError('Staged binary checksum mismatch')
workerbin=root/'cnc-worker';scanner=root/'tools/cnc-enum';manifest=root/'tools/.cnc-manifest.json'
if sha(Path('/proc')/before['MainPID']/'exe')!=sha(workerbin):raise RuntimeError('Worker installed/running mismatch')
for path,label in [(workerbin,'cnc-worker.previous'),(scanner,'cnc-enum.previous'),(manifest,'tool-manifest.previous')]:
 if path.exists() and not (folder/label).exists():shutil.copy2(path,folder/label)
try:
 for name,target in [('cnc-worker',workerbin),('cnc-enum',scanner)]:
  temp=target.with_name(target.name+'.runtime-new');shutil.copy2(folder/(name+'.new'),temp);temp.chmod(0o700);temp.replace(target)
 if manifest.exists():
  metadata=json.loads(manifest.read_text());entry=next(f for f in metadata['files'] if f['path']=='cnc-enum');entry['sha256']=digests['cnc-enum']
  temp=manifest.with_name(manifest.name+'.runtime-new');temp.write_text(json.dumps(metadata,sort_keys=True));temp.chmod(0o600);temp.replace(manifest)
 subprocess.run(['systemctl','restart','--no-block','cnc-worker'],check=True)
 deadline=time.monotonic()+55
 while True:
  now=state()
  if now['ActiveState']=='active' and now['SubState']=='running' and now['MainPID']!=before['MainPID'] and now['ControlPID']=='0' and now.get('Job','') in ('','0'):
   if sha(Path('/proc')/now['MainPID']/'exe')==digests['cnc-worker'] and sha(scanner)==digests['cnc-enum']:break
  if time.monotonic()>deadline:raise RuntimeError('Graceful restart pending; no forced shutdown attempted')
  time.sleep(1)
except BaseException:
 for path,label in [(workerbin,'cnc-worker.previous'),(scanner,'cnc-enum.previous'),(manifest,'tool-manifest.previous')]:
  if (folder/label).exists():
   temp=path.with_name(path.name+'.runtime-rollback');shutil.copy2(folder/label,temp);temp.replace(path)
 raise
receipt={'worker':worker,'worker_sha256':sha(Path('/proc')/now['MainPID']/'exe'),'scanner_sha256':sha(scanner),'rollback':str(folder),'drain_preserved':True,'private_tools_accessed':False}
(folder/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n');print(json.dumps(receipt))
'''

def main():
 parser=argparse.ArgumentParser(description=__doc__)
 parser.add_argument('--stage',type=Path,required=True)
 parser.add_argument('--inventory',type=Path,required=True)
 parser.add_argument('--apply',action='store_true',help='Requires explicit owner production approval')
 args=parser.parse_args();manifest=json.loads((args.stage/'manifest.json').read_text());release=manifest['release']
 if not release.replace('-','').isalnum():raise RuntimeError('Unsafe release ID')
 workers=json.loads(args.inventory.read_text());expected=set(json.loads((ROOT/'.linode/active-deployment.json').read_text())['workers'])
 if {w['id'] for w in workers}!=expected or len(workers)!=len(expected):raise RuntimeError('Inventory must match active deployment')
 digests={name:manifest['sha256'][name] for name in ['cnc-worker','cnc-enum']}
 for name,digest in digests.items():
  if hashlib.sha256((args.stage/name).read_bytes()).hexdigest()!=digest:raise RuntimeError('Local artifact changed')
 if not args.apply:
  print(json.dumps({'release':release,'dry_run':True,'workers':sorted(expected),'binaries':digests,'private_tools_accessed':False}));return
 ssh=['ssh','-i',str(ROOT/'.linode/id_ed25519'),'-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile='+str(ROOT/'.linode/cross-cloud-known_hosts'),'-o','ConnectTimeout=10']
 scp=['scp','-q']+ssh[1:]
 results=[]
 for item in workers:
  worker,address=item['id'],item['address'];target='root@'+address;folder='/opt/cnc/rollouts/'+release
  subprocess.run(ssh+[target,'install -d -m 700 '+folder],check=True,timeout=20)
  for name in digests:subprocess.run(scp+[str(args.stage/name),target+':'+folder+'/'+name+'.new'],check=True,timeout=60)
  payload='release='+repr(release)+'\nworker='+repr(worker)+'\ndigests='+repr(digests)+'\n'+REMOTE
  result=subprocess.run(ssh+[target,'python3 -'],input=payload,text=True,capture_output=True,timeout=80)
  if result.returncode:raise RuntimeError(worker+': '+result.stderr[-1200:])
  receipt=json.loads(result.stdout);results.append(receipt);print(json.dumps(receipt),flush=True)
  (args.stage/'worker-verification.json').write_text(json.dumps(results,indent=2)+'\n')
if __name__=='__main__':main()

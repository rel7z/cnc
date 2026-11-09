#!/usr/bin/env python3
"""Add just DNSRift and its manifest entry without restarting busy workers."""
import concurrent.futures,json,subprocess,time
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2];state=json.loads((ROOT/'.aws-cnc/state.json').read_text());cli=str(Path.home()/'.local/bin/aws')
SHA='352f9e1f7a902c78cec94584d908c5525b2707b1847ee810ab43c5b99a48c631'
script=r'''import hashlib,io,json,os,pathlib,shutil,subprocess,tarfile,time,urllib.request
P=pathlib.Path;root=P('/opt/cnc/tools');target=root/'reverseip-dnsrift';manifest=root/'.cnc-manifest.json'
assert manifest.is_file() and not manifest.is_symlink(),'Generic manifest missing'
o=urllib.request.build_opener(urllib.request.ProxyHandler({}))
with o.open('http://172.31.4.149:8080/download/cnc-worker-tools',timeout=120) as r:archive=r.read()
with tarfile.open(fileobj=io.BytesIO(archive),mode='r:gz') as package:
 member=package.getmember('tools/reverseip-dnsrift');assert member.isfile(),'Expected a regular DNSRift binary';data=package.extractfile(member).read()
assert hashlib.sha256(data).hexdigest()==EXPECTED,'DNSRift download checksum mismatch'
backup=P('/opt/cnc/dnsrift-worker-rollback')/str(time.time_ns());backup.mkdir(parents=True,mode=0o700)
shutil.copy2(manifest,backup/'manifest.json')
if target.exists():shutil.copy2(target,backup/'reverseip-dnsrift')
temp=target.with_suffix('.new');temp.write_bytes(data);temp.chmod(0o700);os.replace(temp,target)
check=subprocess.run([str(target),'--help'],capture_output=True,text=True,timeout=10);assert check.returncode==0 and '-ip' in check.stderr,'DNSRift help failed'
meta=json.loads(manifest.read_text());meta['files']=[f for f in meta['files'] if f['path']!='reverseip-dnsrift'];meta['files'].append({'path':'reverseip-dnsrift','sha256':EXPECTED,'mode':448})
if 'reverseip-dnsrift' not in meta['required_executables']:meta['required_executables'].append('reverseip-dnsrift')
temp=manifest.with_suffix('.new');temp.write_text(json.dumps(meta,sort_keys=True));temp.chmod(0o600);os.replace(temp,manifest)
print(json.dumps({'binary_sha256':EXPECTED,'offline_help_passed':True,'worker_restarted':False,'rollback':str(backup)}))'''.replace('EXPECTED',repr(SHA))
def aws(*args):
 r=subprocess.run([cli,*args,'--profile','cnc','--region',state['region'],'--output','json'],capture_output=True,text=True,check=True);return json.loads(r.stdout)
nodes={k:v['id'] for k,v in state['nodes'].items() if k.startswith('cnc-aws-worker-')}
command="python3 - <<'INSTALL'\n"+script+"\nINSTALL\n"
command_id=aws('ssm','send-command','--instance-ids',*nodes.values(),'--document-name','AWS-RunShellScript','--parameters',json.dumps({'commands':[command],'executionTimeout':['120']}))['Command']['CommandId'];print('SSM workers:',command_id,flush=True)
def amazon(item):
 name,node=item
 while True:
  try:r=aws('ssm','get-command-invocation','--command-id',command_id,'--instance-id',node)
  except subprocess.CalledProcessError:time.sleep(2);continue
  if r['Status'] not in ('Pending','InProgress','Delayed'):break
  time.sleep(3)
 if r['Status']!='Success':return {'worker':name,'ok':False,'error':r.get('StandardErrorContent','')[-700:]}
 return {'worker':name,'ok':True,**json.loads(r['StandardOutputContent'].strip().splitlines()[-1])}
def linode(i):
 name='cnc-linode-worker-'+str(i);r=subprocess.run(['ssh','-F',str(ROOT/'.linode/cross-cloud-ssh_config'),name,'python3','-'],input=script,capture_output=True,text=True,timeout=150)
 if r.returncode:return {'worker':name,'ok':False,'error':r.stderr[-700:]}
 return {'worker':name,'ok':True,**json.loads(r.stdout.strip().splitlines()[-1])}
with concurrent.futures.ThreadPoolExecutor(max_workers=11) as pool:
 futures=[pool.submit(amazon,item) for item in nodes.items()]+[pool.submit(linode,i) for i in range(1,9)]
 results=[future.result() for future in futures]
p=ROOT/'.aws-cnc/dnsrift-workers.json';p.write_text(json.dumps(results,indent=2));p.chmod(0o600)
print(json.dumps(results,indent=2));raise SystemExit(0 if all(r['ok'] for r in results) else 1)

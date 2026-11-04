#!/usr/bin/env python3
"""Provision the requested Linode cluster, retaining state for safe resumption."""
import argparse,json,os,secrets,subprocess,time,urllib.request,urllib.error,sys,base64
from pathlib import Path
sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
from worker_login_policy import default_worker_password,password_setup
from ssh_identity import deployment_key,identity_lines
from ssh_policy import key_only_setup
ROOT=Path(__file__).resolve().parents[2]
STATE=ROOT/'.linode'

def api(path,payload=None):
 token=os.environ.get('LINODE_TOKEN')
 if not token: raise RuntimeError('Set LINODE_TOKEN')
 request=urllib.request.Request('https://api.linode.com/v4/'+path,data=None if payload is None else json.dumps(payload).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
 try:
  with urllib.request.urlopen(request,timeout=45) as response:return json.load(response)
 except urllib.error.HTTPError as error:raise RuntimeError(f'Linode {error.code}: {error.read().decode()}') from None

def main():
 parser=argparse.ArgumentParser(description=__doc__)
 parser.add_argument('--region',default='sg-sin-2');parser.add_argument('--workers',type=int,default=6)
 parser.add_argument('--server-type',default='g6-standard-4');parser.add_argument('--worker-type',default='g6-standard-2')
 parser.add_argument('--apply',action='store_true');args=parser.parse_args()
 STATE.mkdir(mode=0o700,exist_ok=True);STATE.chmod(0o700)
 path=STATE/'state.json';state=json.loads(path.read_text()) if path.exists() else {'servers':{}}
 def save():
  tmp=path.with_suffix('.tmp');tmp.write_text(json.dumps(state,indent=2)+'\n');tmp.chmod(0o600);tmp.replace(path)
 types=api('linode/types?page_size=500')['data'];plans={t['id']:t for t in types}
 cost=plans[args.server_type]['price']['monthly']+args.workers*plans[args.worker_type]['price']['monthly']
 print(json.dumps({'region':args.region,'server_ram_mb':plans[args.server_type]['memory'],'worker_ram_mb':plans[args.worker_type]['memory'],'workers':args.workers,'monthly_usd':cost}),flush=True)
 if not args.apply:return
 key=deployment_key(ROOT,STATE)
 existing=api('linode/instances?page_size=500')['data']
 names=['cnc-linode-server']+[f'cnc-linode-worker-{i}' for i in range(1,args.workers+1)]
 for index,name in enumerate(names):
  instance=next((i for i in existing if i['label']==name),None)
  target=args.server_type if index==0 else args.worker_type
  if instance:
   if instance['region']!=args.region or instance['type']!=target:raise RuntimeError('Existing node configuration mismatch: '+name)
  else:
   password=default_worker_password(ROOT) if index>0 else None
   payload={'label':name,'region':args.region,'type':target,'image':'linode/ubuntu24.04','root_pass':password or secrets.token_urlsafe(36)+'aA1!','authorized_keys':[key.with_suffix('.pub').read_text().strip()],'private_ip':True,'booted':True,'tags':['cnc','cnc-managed']}
   setup=key_only_setup()+(password_setup(password) if password else '')
   payload['metadata']={'user_data':base64.b64encode(('#!/bin/bash\nset -eu\n'+setup).encode()).decode()}
   instance=api('linode/instances',payload)
  state['servers'][name]={'id':instance['id'],'public_ip':next((ip for ip in instance['ipv4'] if not ip.startswith('192.168.')),None),'private_ip':next((ip for ip in instance['ipv4'] if ip.startswith('192.168.')),None),'type':instance['type'],'region':instance['region']}
  save();print('Provisioned '+name+': '+str(instance['id']),flush=True)
 config=STATE/'ssh_config'
 config.write_text('Host cnc-linode-*\n User root\n'+identity_lines(key,STATE/'id_ed25519')+f' IdentitiesOnly yes\n BatchMode yes\n ConnectTimeout 10\n StrictHostKeyChecking accept-new\n UserKnownHostsFile {STATE/"known_hosts"}\n'+''.join(f'Host {name}\n HostName {node["public_ip"]}\n' for name,node in state['servers'].items()))
 config.chmod(0o600)
 print(json.dumps({'server_ip':state['servers']['cnc-linode-server']['public_ip'],'nodes_created':len(names)}),flush=True)
if __name__=='__main__':main()

#!/usr/bin/env python3
"""Provision owner-approved CNC DigitalOcean nodes with private resumable state."""
import argparse,json,subprocess,urllib.request,urllib.error,sys
from pathlib import Path
sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
from ssh_identity import deployment_key,identity_lines
from ssh_policy import key_only_setup
ROOT=Path(__file__).resolve().parents[2]
STATE=ROOT/'.digitalocean'
CLUSTER='cnc-cluster-20261101'
NODES={'controller':'cnc-do-controller','worker-1':'cnc-do-worker-1','worker-2':'cnc-do-worker-2'}
class APIError(RuntimeError):pass

def api(path,payload=None,method=None):
 token=(STATE/'token').read_text().strip()
 request=urllib.request.Request('https://api.digitalocean.com/v2/'+path,data=None if payload is None else json.dumps(payload).encode(),method=method,headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
 try:
  with urllib.request.urlopen(request,timeout=45) as response:
   data=response.read();return json.loads(data) if data else {}
 except urllib.error.HTTPError as error:raise APIError('DigitalOcean HTTP '+str(error.code)+' for '+path.split('?')[0]) from None
 except (TimeoutError,urllib.error.URLError):raise APIError('DigitalOcean response uncertain; reconcile resources before retrying') from None

def private_json(path,data):
 temp=path.with_suffix('.tmp');temp.write_text(json.dumps(data,indent=2)+'\n');temp.chmod(0o600);temp.replace(path)

def write_ssh(state,key):
 # Keep one public entry point; the other nodes use the private VPC.
 nodes=state['nodes'];jump='cnc-do-worker-1'
 text='Host cnc-do-*\n User root\n'+identity_lines(key,STATE/'id_ed25519')+f' IdentitiesOnly yes\n BatchMode yes\n ConnectTimeout 10\n StrictHostKeyChecking accept-new\n UserKnownHostsFile "{STATE/"known_hosts"}"\n'
 for role,node in nodes.items():
  private=node.get('private_ip') if role!='worker-1' and 'worker-1' in nodes else None
  host=private or node.get('public_ip')
  if not host:raise RuntimeError('Node SSH address unavailable: '+node['name'])
  text+='Host '+node['name']+'\n HostName '+host+'\n'
  if private:text+=' ProxyJump '+jump+'\n'
 path=STATE/'ssh_config';temp=path.with_suffix('.tmp');temp.write_text(text);temp.chmod(0o600);temp.replace(path)

def main():
 parser=argparse.ArgumentParser(description=__doc__)
 parser.add_argument('--roles',nargs='+',choices=NODES,required=True)
 parser.add_argument('--apply',action='store_true')
 args=parser.parse_args();STATE.mkdir(mode=0o700,exist_ok=True);STATE.chmod(0o700)
 account=api('account')['account'];droplets=api('droplets?per_page=200')['droplets'];sizes=api('sizes?per_page=200')['sizes']
 size=next(s for s in sizes if s['slug']=='s-4vcpu-8gb' and s['memory']==8192 and s['available'] and 'sgp1' in s['regions'])
 if size['price_monthly']>48:raise RuntimeError('8 GB plan exceeds approved $48/month price')
 state=json.loads((STATE/'state.json').read_text()) if (STATE/'state.json').exists() else {'cluster':CLUSTER,'nodes':{}}
 missing=[]
 for role in args.roles:
  existing=next((d for d in droplets if d['name']==NODES[role]),None)
  if existing:
   if CLUSTER not in existing['tags'] or existing['region']['slug']!='sgp1' or existing['size_slug']!=size['slug']:raise RuntimeError('Conflicting node; refusing changes: '+NODES[role])
   state['nodes'][role]={'id':existing['id'],'name':existing['name']}
  else:missing.append(role)
 if len(droplets)+len(missing)>account['droplet_limit']:raise RuntimeError('Droplet limit prevents requested batch; no resources changed')
 print(json.dumps({'roles':args.roles,'new_servers':len(missing),'memory_mb':8192,'vcpus':4,'monthly_per_server':size['price_monthly'],'region':'sgp1','apply':args.apply}),flush=True)
 if not args.apply:return
 keyfile=deployment_key(ROOT,STATE)
 keyfile.chmod(0o600);public=keyfile.with_suffix('.pub').read_text().strip()
 keys=api('account/keys?per_page=200')['ssh_keys'];key=next((k for k in keys if k['public_key'].split()[:2]==public.split()[:2]),None)
 if key is None:key=api('account/keys',{'name':'cnc-digitalocean-admin-20261101','public_key':public})['ssh_key']
 for role in missing:
  name=NODES[role]
  cloud={'hostname':name,'disable_root':False,'ssh_pwauth':False,'ssh_authorized_keys':[public],
   'package_update':True,'packages':['python3','curl','ca-certificates','xz-utils'],'timezone':'Asia/Jakarta',
   'runcmd':[['bash','-se','-c',key_only_setup()]]}
  payload={'name':name,'region':'sgp1','size':size['slug'],'image':'ubuntu-24-04-x64','ssh_keys':[key['id']],
   'backups':False,'ipv6':False,'monitoring':True,'tags':[CLUSTER,'cnc-controller' if role=='controller' else 'cnc-worker'],
   'user_data':'#cloud-config\n'+json.dumps(cloud)}
  state['pending']=name;private_json(STATE/'state.json',state)
  response=api('droplets',payload)['droplet'];state['nodes'][role]={'id':response['id'],'name':name};state.pop('pending',None);private_json(STATE/'state.json',state)
  print(json.dumps({'created':name,'id':response['id'],'memory_mb':response['memory'],'role':role}),flush=True)
 for role in args.roles:
  node=api('droplets/'+str(state['nodes'][role]['id']))['droplet']
  addresses={n['type']:n['ip_address'] for n in node['networks']['v4']}
  state['nodes'][role].update(status=node['status'],public_ip=addresses.get('public'),private_ip=addresses.get('private'),memory_mb=node['memory'],vcpus=node['vcpus'])
 state['ssh_identity_file']=str(keyfile)
 private_json(STATE/'state.json',state)
 write_ssh(state,keyfile)
 print(json.dumps({'state_saved':True,'nodes':state['nodes']}),flush=True)
if __name__=='__main__':main()

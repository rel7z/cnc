#!/usr/bin/env python3
"""Use the same verified generic package installer on existing AWS workers."""
import ast,base64,concurrent.futures,json,subprocess,time
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2];state=json.loads((ROOT/'.aws-cnc/state.json').read_text());cli=str(Path.home()/'.local/bin/aws')
tree=ast.parse((ROOT/'infra/aws/sync-generic-worker-tools.py').read_text())
assignment=next(n for n in tree.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='script' for t in n.targets))
script=ast.literal_eval(assignment.value.func.value).replace('CHECK',base64.b64encode((ROOT/'cnc-api/worker_tools_manifest.py').read_bytes()).decode())
command="python3 - <<'SYNC'\n"+script+"\nSYNC\n"
nodes={k:v['id'] for k,v in state['nodes'].items() if k.startswith('cnc-aws-worker-')}
def aws(*args):
 r=subprocess.run([cli,*args,'--profile','cnc','--region','us-east-2','--output','json'],capture_output=True,text=True,check=True);return json.loads(r.stdout)
id=aws('ssm','send-command','--instance-ids',*nodes.values(),'--document-name','AWS-RunShellScript','--parameters',json.dumps({'commands':[command],'executionTimeout':['240']}))['Command']['CommandId'];print('SSM command:',id,flush=True)
def wait(item):
 name,node=item
 while True:
  try:r=aws('ssm','get-command-invocation','--command-id',id,'--instance-id',node)
  except subprocess.CalledProcessError:time.sleep(2);continue
  if r['Status'] not in ('Pending','InProgress','Delayed'):break
  time.sleep(3)
 if r['Status']!='Success':return {'worker':name,'ok':False,'status':r['Status'],'error':r.get('StandardErrorContent','')[-700:]}
 return {'worker':name,'ok':True,**json.loads(r['StandardOutputContent'].strip().splitlines()[-1])}
with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:results=list(pool.map(wait,nodes.items()))
print(json.dumps(results,indent=2));p=ROOT/'.aws-cnc/generic-tools-verification.json';p.write_text(json.dumps(results,indent=2));p.chmod(0o600)
raise SystemExit(0 if all(r['ok'] for r in results) else 1)

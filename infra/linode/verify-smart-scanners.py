#!/usr/bin/env python3
"""Owned six-worker CNC/Drive proof, after activation and only while idle."""
import importlib.util,json,pathlib
ROOT=pathlib.Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('rollout',ROOT/'infra/linode/rollout-smart-scanners.py');rollout=importlib.util.module_from_spec(spec);spec.loader.exec_module(rollout)
PROOF=r'''
import http.cookiejar,http.server,os,shlex,tempfile,threading,urllib.error,urllib.parse,uuid
manifest=json.loads((release/'manifest.json').read_text())
assert digest('/opt/cnc/cnc-server')==manifest['binaries']['cnc-server'],'Release is staged but not activated'
w=workers();assert len(w)==6 and all(x['status']=='online' and x['current_load']==0 for x in w),'Workers must be idle for fixtures'
config=json.loads(pathlib.Path('/opt/cnc/config.json').read_text());drive=config['gdrive'];merged=pathlib.Path(os.path.expanduser(drive['watch_dir']))
routes=json.loads(pathlib.Path('/opt/cnc-processor/config.json').read_text()).get('routes',{})
assert 'drupal.txt' not in routes,'Fixture output would trigger a downstream tool'
output=merged/'drupal.txt';assert not output.exists(),'Existing Drupal output; do not mix fixture results'
login={}
for line in pathlib.Path('/opt/cnc/login.env').read_text().splitlines():
 if '=' in line and not line.startswith('#'):
  k,v=line.split('=',1);parts=shlex.split(v);login[k]=parts[0] if len(parts)==1 else v
opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
base='http://127.0.0.1:3000'
def dashboard(path,method='GET',body=None):
 r=urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),method=method,headers={'Origin':base,'Content-Type':'application/json'})
 return json.load(opener.open(r,timeout=20))
dashboard('/api/auth/login','POST',{'username':login.get('CNC_LOGIN_USERNAME','fahrel'),'password':login['CNC_LOGIN_PASSWORD']})
# Refresh only for read-only verification; never print or rewrite credentials.
token=json.loads(pathlib.Path(os.path.expanduser(drive['token_path'])).read_text())
def drive_read(path):
 global token
 for attempt in range(2):
  request=urllib.request.Request('https://www.googleapis.com/drive/v3/'+path,headers={'Authorization':'Bearer '+token['access_token']})
  try:
   with urllib.request.urlopen(request,timeout=5) as response:return response.read()
  except urllib.error.HTTPError as e:
   if e.code!=401 or attempt:raise
   payload=urllib.parse.urlencode({'grant_type':'refresh_token','refresh_token':token['refresh_token'],'client_id':drive['client_id'],'client_secret':drive['client_secret']}).encode()
   with urllib.request.urlopen(urllib.request.Request('https://oauth2.googleapis.com/token',data=payload),timeout=15) as response:token.update(json.load(response))
query="'%s' in parents and name = 'drupal.txt' and trashed = false"%drive['parent_folder_id']
def drive_output():
 listing=json.loads(drive_read('files?'+urllib.parse.urlencode({'q':query,'fields':'files(id,name)','pageSize':100})))['files']
 if not listing:return None,None
 assert len(listing)==1,'Unexpected duplicate fixture filename'
 return listing[0]['id'],drive_read('files/'+listing[0]['id']+'?alt=media').decode()
assert not drive_output()[0],'Existing Drive Drupal output; do not mix fixture results'
nonce=uuid.uuid4().hex[:12]
class Fixture(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_GET(self):
  if '/slow/' in self.path:time.sleep(35)
  if self.path=='/wp-content/plugins/owned/readme.txt':
   self.send_response(200);self.send_header('Content-Type','text/plain');self.end_headers();self.wfile.write(b'=== Owned fixture ===\nStable tag: 2.0.0\n');return
  if self.path.startswith('/'+nonce+'/'):
   self.send_response(200);self.send_header('Content-Type','text/html');self.send_header('X-Generator','Drupal 10');self.end_headers();self.wfile.write(b'<meta name="generator" content="Drupal 10">');return
  self.send_response(404);self.end_headers();self.wfile.write(b'missing')
fixture=http.server.ThreadingHTTPServer(('192.168.145.81',0),Fixture);threading.Thread(target=fixture.serve_forever,daemon=True).start()
initial=api('health')['enabled'];api('processing-state',{'enabled':False})
proof={'release':RELEASE,'fixture_only':True,'jobs':[]};started=[]
try:
 with tempfile.TemporaryDirectory(prefix='cnc-smart-input-') as tmp:
  for tool in ['cms-scan','enum']:
   source=pathlib.Path(tmp)/(tool+'.txt')
   # Enum uses fixed 1,000-line chunks rather than one partition per worker.
   # Repeated owned URLs create six chunks while each worker deduplicates to two requests.
   repetitions=500 if tool=='enum' else 1
   source.write_text(''.join(f'http://192.168.145.81:{fixture.server_port}/{nonce}/{tool}/{speed}/{n}\n'*repetitions for n in range(6) for speed in ['fast','slow']))
   payload={'tool_id':tool,'input_file':str(source),'timeout_seconds':-1,'options':{'threads':1,'auto_concurrency':True,'timeout':60}}
   if tool=='enum':payload['target_authorization']={'acknowledged':True,'scope':{'hosts':[],'cidrs':['192.168.145.81/32'],'schemes':['http'],'ports':[fixture.server_port]}}
   launched=dashboard('/api/tools/launch','POST',payload);job=launched['job_id'];started.append(job)
   deadline=time.monotonic()+150;live=False;drive_id=None
   while time.monotonic()<deadline:
    status=dashboard('/api/jobs/'+job)
    if isinstance(status,dict) and 'job' in status:status=status['job']
    content=output.read_text() if output.exists() else ''
    if f'/{nonce}/{tool}/fast/' in content and status['status']=='running':
     try:
      drive_id,uploaded=drive_output()
      if uploaded and f'/{nonce}/{tool}/fast/' in uploaded:live=True
     except (TimeoutError,urllib.error.URLError):pass
    if status['status'] in ('completed','completed_with_errors','failed','cancelled'):break
    time.sleep(1)
   else:raise RuntimeError('Fixture job did not finish within 150 seconds')
   assert status['status']=='completed',(job,status['status'])
   tasks=dashboard('/api/jobs/'+job+'/tasks');tasks=tasks.get('tasks',[]) if isinstance(tasks,dict) else tasks
   assigned={t['assigned_to'] for t in tasks};assert len(tasks)==6 and len(assigned)==6,(job,assigned)
   assert all(t['status']=='completed' for t in tasks),(job,[t['status'] for t in tasks])
   assert live,'Merged/Drive output was not seen while fixture execution was running'
   proof['jobs'].append({'tool':tool,'job_id':job,'parts':6,'workers':sorted(assigned),'live_merged_and_drive':live,'drive_fixture_file_id':drive_id,'status':'completed'})
   # Keep the same owned output file for the second phase. No downstream route matches it.
  assert output.exists() and all(nonce in line for line in output.read_text().splitlines()),'Fixture filename contains unrelated results'
  assert len(set(output.read_text().splitlines()))==24,'Missing or duplicate accepted fixture results'
finally:
 # Cancel only verifier-owned jobs if verification fails, then drain them before source cleanup.
 for job in started:
  try:
   status=dashboard('/api/jobs/'+job)
   if status.get('status') in ('running','queued','pending'):dashboard('/api/jobs/'+job+'/cancel','POST',{})
  except Exception:pass
 fixture.shutdown();fixture.server_close()
 if output.exists() and all(nonce in line for line in output.read_text().splitlines()):output.unlink()
 api('processing-state',{'enabled':initial})
proof['processing_restored']=initial;proof['workers_online']=sum(w['status']=='online' for w in workers());proof['private_tools_accessed']=False
print(json.dumps(proof))
'''
if __name__=='__main__':
 manifest=json.loads((rollout.STAGE/'manifest.json').read_text())
 result=json.loads(rollout.remote(rollout.SERVER,rollout.COMMON+PROOF,RELEASE=manifest['release'],SERVER=True))
 (ROOT/'processor/evidence/cms-smart-production-20261014.json').write_text(json.dumps(result,indent=2)+'\n')
 print(json.dumps(result,indent=2))

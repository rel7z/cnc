#!/usr/bin/env python3
"""Deploy the processor form and downloads, preserving worker binaries and private pins."""
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile
import time

from importlib.util import spec_from_file_location, module_from_spec
ROOT = Path(__file__).resolve().parents[2]
STAGE = Path('/tmp/cnc-processor-editor-release')
spec = spec_from_file_location('friendly_rollout', ROOT/'infra/linode/rollout-friendly-version-ui.py')
ui = module_from_spec(spec)
spec.loader.exec_module(ui)
SSH_OPTIONS = ['-o','ControlMaster=auto','-o','ControlPersist=120','-o','ControlPath=/tmp/cnc-templates-%C']
ui.SSH += SSH_OPTIONS

ACTIVATE = r'''
import hashlib,http.cookiejar,json,shlex,shutil,sqlite3,subprocess,tarfile,time,urllib.request,urllib.error,uuid
from pathlib import Path
release=Path(RELEASE)
assert hashlib.sha256((release/'ui.tar.gz').read_bytes()).hexdigest()==UI_DIGEST
assert hashlib.sha256((release/'cnc-processor').read_bytes()).hexdigest()==PROCESSOR_DIGEST
with tarfile.open(release/'ui.tar.gz') as archive:
 assert all((m.isfile() or m.isdir()) and m.name.startswith('ui/') for m in archive.getmembers())
 archive.extractall(release,filter='data')
settings={}
for line in Path('/opt/cnc/processor.env').read_text().splitlines():
 if '=' in line and not line.startswith('#'):
  key,value=line.split('=',1);settings[key]=value.strip('"')
headers={'Authorization':'Bearer '+settings['CNC_PROCESSOR_TOKEN'],'Content-Type':'application/json'}
def api(path,method='GET',body=None):
 request=urllib.request.Request('http://127.0.0.1:9191/v1/'+path,headers=headers,method=method,data=None if body is None else json.dumps(body).encode())
 with urllib.request.urlopen(request,timeout=10) as response:return json.load(response)
initial=api('health')['enabled']
api('processing-state','PUT',{'enabled':False})
backup=release/'rollback';backup.mkdir(exist_ok=True);backup.chmod(0o700)
config=Path('/etc/systemd/system/cnc-ui.service.d/zz-enum-version-pipeline.conf')
original=config.read_bytes();(backup/'ui.conf').write_bytes(original)
processor=Path('/opt/cnc-processor/cnc-processor')
shutil.copy2(processor,backup/'cnc-processor')
shutil.copy2('/opt/cnc-processor/config.json',backup/'config.json')
stopped=False
try:
 deadline=time.monotonic()+600
 while api('health')['counts'].get('running',0):
  if time.monotonic()>deadline:raise RuntimeError('active jobs did not drain; rollout cancelled')
  time.sleep(3)
 subprocess.run(['systemctl','stop','cnc-processor'],check=True);stopped=True
 with sqlite3.connect('/opt/cnc-processor/processor.db') as db,sqlite3.connect(backup/'processor.db') as dest:db.backup(dest)
 (backup/'processor.db').chmod(0o600)
 temporary=processor.with_name('cnc-processor.templates-new');shutil.copy2(release/'cnc-processor',temporary);temporary.chmod(0o700);temporary.replace(processor)
 subprocess.run(['systemctl','start','cnc-processor'],check=True)
 for attempt in range(30):
  try:api('processor-templates');api('processor-setups');break
  except Exception:
   if attempt==29:raise
   time.sleep(1)
 entry=release/'ui/.next/standalone/server.js';assert entry.is_file()
 lines=[line for line in original.decode().splitlines() if not line.startswith(('ExecStart=','WorkingDirectory='))]
 if 'EnvironmentFile=/opt/cnc/processor.env' not in lines:lines.append('EnvironmentFile=/opt/cnc/processor.env')
 lines+=['ExecStart=','ExecStart=/opt/cnc/node-runtime/node-v22.23.3-linux-x64/bin/node '+str(entry),'WorkingDirectory='+str(entry.parent)]
 temporary=config.with_name(config.name+'.new');temporary.write_text('\n'.join(lines)+'\n');temporary.chmod(config.stat().st_mode&0o777);temporary.replace(config)
 subprocess.run(['systemctl','daemon-reload'],check=True);subprocess.run(['systemctl','restart','cnc-ui'],check=True)
 for attempt in range(30):
  try:
   with urllib.request.urlopen('http://127.0.0.1:3000/login',timeout=5) as response:assert response.status==200
   try:urllib.request.urlopen('http://127.0.0.1:3000/api/version-check-templates',timeout=5);raise RuntimeError('unauthenticated templates exposed')
   except urllib.error.HTTPError as error:assert error.code==401
   break
  except Exception:
   if attempt==29:raise
   time.sleep(1)
 login={}
 for line in Path('/opt/cnc/login.env').read_text().splitlines():
  if '=' in line and not line.startswith('#'):
   key,value=line.split('=',1);parts=shlex.split(value);login[key]=parts[0] if len(parts)==1 else value
 opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
 dashboard='http://127.0.0.1:3000'
 def dashboard_request(path,method='GET',body=None):
  request=urllib.request.Request(dashboard+path,method=method,data=None if body is None else json.dumps(body).encode(),headers={'Origin':dashboard,'Content-Type':'application/json'})
  with opener.open(request,timeout=10) as response:return json.load(response)
 dashboard_request('/api/auth/login','POST',{'username':login.get('CNC_LOGIN_USERNAME','fahrel'),'password':login.get('CNC_LOGIN_PASSWORD','fahrel')})
 assert len(dashboard_request('/api/tools'))>0,'dashboard tool forwarding failed'
 definition={'tool_id':'editor-deploy-fixture','incoming':'editor-deploy.txt','folder':'','manifest':{'scripts':{'scanner':{'path':'scanner.py','outputs':['results.txt']}}}}
 smoke=dashboard_request('/api/processor/editor/templates','POST',{'name':'Editor deployment fixture '+uuid.uuid4().hex,'definition':definition})
 smoke_path='/api/processor/editor/templates/'+smoke['id']
 updated=dashboard_request(smoke_path,'PUT',{'name':smoke['name']+' updated','definition':definition,'revision':1})
 assert updated['revision']==2
 assert dashboard_request(smoke_path,'DELETE',{'revision':2})['deleted']
 assert isinstance(dashboard_request('/api/processor/editor/results'),list)
 assert 'revision' in dashboard_request('/api/processor/editor/setups')
 api('processing-state','PUT',{'enabled':initial})
except Exception:
 if stopped:
  subprocess.run(['systemctl','stop','cnc-processor'],check=True)
  shutil.copy2(backup/'cnc-processor',processor)
  # Additive schema is safe for the previous binary; retain all accepted data.
  subprocess.run(['systemctl','start','cnc-processor'],check=True)
 config.write_bytes(original);subprocess.run(['systemctl','daemon-reload'],check=True);subprocess.run(['systemctl','restart','cnc-ui'],check=True)
 api('processing-state','PUT',{'enabled':initial})
 raise
print(json.dumps({'rollback_directory':str(backup),'database_backed_up':True,'intake_restored':initial,'processor_templates_available':True,'unauthenticated_templates':401,'services_restarted':['cnc-processor','cnc-ui']}))
'''

def main():
 STAGE.mkdir(exist_ok=True);STAGE.chmod(0o700)
 processor=STAGE/'cnc-processor';assert processor.is_file()
 routes=json.loads((ROOT/'cnc-ui/.next/routes-manifest.json').read_text())['rewrites']
 if isinstance(routes,dict):routes=sum(routes.values(),[])
 expected=ui.remote('cnc-linode-server',"import json\nfrom pathlib import Path\nprint(json.loads(Path('/opt/cnc/config.json').read_text())['http_addr'])").strip()
 assert any(r.get('destination')=='http://'+expected+'/api/:path*' for r in routes),'Build dashboard with GO_API_URL=http://'+expected
 archive=STAGE/'ui.tar.gz'
 with tarfile.open(archive,'w:gz') as tar:
  for source,destination in [(ROOT/'cnc-ui/.next/standalone','ui/.next/standalone'),(ROOT/'cnc-ui/.next/static','ui/.next/standalone/.next/static'),(ROOT/'cnc-ui/public','ui/.next/standalone/public')]:
   for path in sorted(source.rglob('*')):
    if path.is_symlink():raise RuntimeError('Unexpected dashboard symlink')
    if path.is_file():tar.add(path,arcname=str(Path(destination)/path.relative_to(source)),recursive=False)
 ui_digest=hashlib.sha256(archive.read_bytes()).hexdigest();processor_digest=hashlib.sha256(processor.read_bytes()).hexdigest()
 release='processor-editor-'+hashlib.sha256((ui_digest+processor_digest).encode()).hexdigest()[:16]
 directory='/opt/cnc/queue-releases/'+release
 before=ui.binary_snapshot();print('Controller and six worker binaries recorded',flush=True)
 ui.remote('cnc-linode-server','from pathlib import Path\np=Path(RELEASE);p.mkdir(parents=True,exist_ok=True);p.chmod(0o700)',RELEASE=directory)
 for file in [archive,processor]:subprocess.run(['scp','-q','-F',str(ROOT/'.linode/ssh_config')]+SSH_OPTIONS+[str(file),'cnc-linode-server:'+directory+'/'+file.name],check=True)
 print(ui.remote('cnc-linode-server',ACTIVATE,RELEASE=directory,UI_DIGEST=ui_digest,PROCESSOR_DIGEST=processor_digest),flush=True)
 after=ui.binary_snapshot();assert before==after,'Controller or worker binary changed'
 result={'release':release,'deployed':True,'ui_sha256':ui_digest,'processor_sha256':processor_digest,'controller_and_workers_unchanged':True,'binary_sha256':after,'rollback_directory':directory+'/rollback','kiro_accessed':False,'private_tools_source_accessed':False}
 (STAGE/'deployment.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result),flush=True)

if __name__=='__main__':main()

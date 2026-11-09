#!/usr/bin/env python3
"""Deploy scheduling ranks with database/service rollback; leave worker binaries intact."""
import hashlib,json,subprocess,tarfile
from pathlib import Path
from importlib.util import spec_from_file_location,module_from_spec
ROOT=Path(__file__).resolve().parents[2]
STAGE=Path('/tmp/cnc-program-priorities-release')
spec=spec_from_file_location('ui_rollout',ROOT/'infra/linode/rollout-friendly-version-ui.py');ui=module_from_spec(spec);spec.loader.exec_module(ui)
OPTIONS=['-o','ControlMaster=no','-o','ControlPath=none']
ui.SSH+=OPTIONS
ACTIVATE=r'''
import hashlib,http.cookiejar,json,shlex,shutil,sqlite3,subprocess,tarfile,time,urllib.request,urllib.error,uuid
from pathlib import Path
release=Path(RELEASE);backup=release/'rollback';backup.mkdir(exist_ok=True);backup.chmod(0o700)
for name,digest in DIGESTS.items():assert hashlib.sha256((release/name).read_bytes()).hexdigest()==digest
with tarfile.open(release/'ui.tar.gz') as archive:
 assert all((m.isfile() or m.isdir()) and m.name.startswith('ui/') and '..' not in Path(m.name).parts for m in archive.getmembers())
 archive.extractall(release,filter='data')
settings={}
for line in Path('/opt/cnc/processor.env').read_text().splitlines():
 if '=' in line and not line.startswith('#'):
  key,value=line.split('=',1);settings[key]=value.strip('"')
headers={'Authorization':'Bearer '+settings['CNC_PROCESSOR_TOKEN'],'Content-Type':'application/json'}
def api(path,method='GET',body=None):
 request=urllib.request.Request('http://127.0.0.1:9191/v1/'+path,headers=headers,method=method,data=None if body is None else json.dumps(body).encode())
 with urllib.request.urlopen(request,timeout=10) as response:return json.load(response)
def workers():
 with urllib.request.urlopen('http://192.168.145.81:8080/api/workers',timeout=10) as response:return json.load(response)
initial=api('health')['enabled'];api('processing-state','PUT',{'enabled':False})
config=Path('/etc/systemd/system/cnc-ui.service.d/zz-enum-version-pipeline.conf');original=config.read_bytes();(backup/'ui.conf').write_bytes(original)
processor=Path('/opt/cnc-processor/cnc-processor');controller=Path('/opt/cnc/cnc-server')
shutil.copy2(processor,backup/'cnc-processor');shutil.copy2(controller,backup/'cnc-server')
changed=False
try:
 deadline=time.monotonic()+600
 while api('health')['counts'].get('running',0) or any(w.get('current_load',0) for w in workers()):
  if time.monotonic()>deadline:raise RuntimeError('active workloads did not drain; rollout cancelled')
  time.sleep(3)
 subprocess.run(['systemctl','stop','cnc-processor'],check=True)
 with sqlite3.connect('/opt/cnc-processor/processor.db') as db,sqlite3.connect(backup/'processor.db') as dest:db.backup(dest)
 (backup/'processor.db').chmod(0o600)
 changed=True
 subprocess.run(['systemctl','stop','cnc-server'],check=True)
 for name,destination in [('cnc-processor',processor),('cnc-server',controller)]:
  temporary=destination.with_name(destination.name+'.priorities-new');shutil.copy2(release/name,temporary);temporary.chmod(0o700);temporary.replace(destination)
 subprocess.run(['systemctl','start','cnc-server'],check=True)
 deadline=time.monotonic()+90
 while True:
  try:
   w=workers()
   if len(w)==6 and all(x['status']=='online' for x in w):break
  except Exception:pass
  if time.monotonic()>deadline:raise RuntimeError('six workers did not reconnect')
  time.sleep(2)
 subprocess.run(['systemctl','start','cnc-processor'],check=True)
 for attempt in range(30):
  try:priorities=api('program-priorities');break
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
   try:urllib.request.urlopen('http://127.0.0.1:3000/api/processor/program-priorities',timeout=5);raise RuntimeError('unauthenticated priorities exposed')
   except urllib.error.HTTPError as error:assert error.code==401
   break
  except Exception:
   if attempt==29:raise
   time.sleep(1)
 login={}
 for line in Path('/opt/cnc/login.env').read_text().splitlines():
  if '=' in line and not line.startswith('#'):
   key,value=line.split('=',1);parts=shlex.split(value);login[key]=parts[0] if len(parts)==1 else value
 opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()));dashboard='http://127.0.0.1:3000'
 def dashboard_request(path,method='GET',body=None,origin=dashboard):
  request=urllib.request.Request(dashboard+path,method=method,data=None if body is None else json.dumps(body).encode(),headers={'Origin':origin,'Content-Type':'application/json'})
  with opener.open(request,timeout=10) as response:return json.load(response)
 dashboard_request('/api/auth/login','POST',{'username':login.get('CNC_LOGIN_USERNAME','fahrel'),'password':login.get('CNC_LOGIN_PASSWORD','fahrel')})
 assert dashboard_request('/api/processor/program-priorities')==priorities
 with urllib.request.urlopen('http://192.168.145.81:8080/api/processor/program-priorities',timeout=10) as response:assert json.load(response)==priorities
 # Test metadata only. No fixture jobs are enqueued and no private tool files are accessed.
 fixture='priority-smoke-'+uuid.uuid4().hex
 with sqlite3.connect('/opt/cnc-processor/processor.db') as db:
  db.execute('INSERT INTO processor_programs VALUES(?,?,?,?,?)',(fixture,'unrestricted-dummy-fixture',1,1,0))
  db.execute('INSERT INTO processor_program_priorities(program_id,configured_rank) VALUES(?,2)',(fixture,))
 try:
  path='/api/processor/program-priorities/'+fixture
  saved=dashboard_request(path,'PUT',{'rank':5,'revision':1});assert saved['effective_rank']==5 and saved['revision']==2
  try:dashboard_request(path,'PUT',{'rank':3,'revision':1});raise RuntimeError('stale save accepted')
  except urllib.error.HTTPError as error:assert error.code==409
  try:dashboard_request(path,'PUT',{'rank':3,'revision':2},'http://example.invalid');raise RuntimeError('cross-origin save accepted')
  except urllib.error.HTTPError as error:assert error.code==403
  reset=dashboard_request(path,'PUT',{'rank':None,'revision':2});assert reset['effective_rank']==2 and reset['override_rank'] is None
 finally:
  with sqlite3.connect('/opt/cnc-processor/processor.db') as db:
   db.execute('DELETE FROM processor_program_priorities WHERE program_id=?',(fixture,));db.execute('DELETE FROM processor_programs WHERE program_id=?',(fixture,))
 api('processing-state','PUT',{'enabled':initial})
except Exception:
 if changed:
  subprocess.run(['systemctl','stop','cnc-processor'],check=True);subprocess.run(['systemctl','stop','cnc-server'],check=True)
  shutil.copy2(backup/'cnc-processor',processor);shutil.copy2(backup/'cnc-server',controller)
  subprocess.run(['systemctl','start','cnc-server'],check=True);subprocess.run(['systemctl','start','cnc-processor'],check=True)
 config.write_bytes(original);subprocess.run(['systemctl','daemon-reload'],check=True);subprocess.run(['systemctl','restart','cnc-ui'],check=True)
 try:api('processing-state','PUT',{'enabled':initial})
 except Exception:pass
 raise
print(json.dumps({'rollback_directory':str(backup),'database_backed_up':True,'processing_restored':initial,'registered_priorities':len(priorities),'unauthenticated':401,'cross_origin':403,'conflict':409,'fixture_save_reset':True,'workers_online':6,'services_restarted':['cnc-processor','cnc-server','cnc-ui']}))
'''
def main():
 STAGE.mkdir(exist_ok=True);STAGE.chmod(0o700)
 routes=json.loads((ROOT/'cnc-ui/.next/routes-manifest.json').read_text())['rewrites']
 if isinstance(routes,dict):routes=sum(routes.values(),[])
 assert any(r.get('destination')=='http://192.168.145.81:8080/api/:path*' for r in routes)
 archive=STAGE/'ui.tar.gz'
 with tarfile.open(archive,'w:gz') as tar:
  for source,destination in [(ROOT/'cnc-ui/.next/standalone','ui/.next/standalone'),(ROOT/'cnc-ui/.next/static','ui/.next/standalone/.next/static'),(ROOT/'cnc-ui/public','ui/.next/standalone/public')]:
   for path in sorted(source.rglob('*')):
    if path.is_symlink():raise RuntimeError('Unexpected dashboard symlink')
    if path.is_file():tar.add(path,arcname=str(Path(destination)/path.relative_to(source)),recursive=False)
 digests={name:hashlib.sha256((STAGE/name).read_bytes()).hexdigest() for name in ['ui.tar.gz','cnc-processor','cnc-server']}
 release='program-priorities-'+hashlib.sha256(json.dumps(digests,sort_keys=True).encode()).hexdigest()[:16];directory='/opt/cnc/queue-releases/'+release
 before=ui.binary_snapshot();print('Controller and all six worker binaries recorded',flush=True)
 ui.remote('cnc-linode-server','from pathlib import Path\np=Path(RELEASE);p.mkdir(parents=True,exist_ok=True);p.chmod(0o700)',RELEASE=directory)
 for name in digests:subprocess.run(['scp','-q','-F',str(ROOT/'.linode/ssh_config')]+OPTIONS+[str(STAGE/name),'cnc-linode-server:'+directory+'/'+name],check=True,timeout=60)
 activation=json.loads(ui.remote('cnc-linode-server',ACTIVATE,RELEASE=directory,DIGESTS=digests));print(json.dumps(activation),flush=True)
 after=ui.binary_snapshot()
 for host in before:
  for path,digest in before[host].items():
   if path!='/opt/cnc/cnc-server':assert after[host][path]==digest,'Worker or scanner binary changed'
 assert after['cnc-linode-server']['/opt/cnc/cnc-server']==digests['cnc-server']
 result={'release':release,'deployed':True,'digests':digests,'activation':activation,'worker_and_scanner_binaries_unchanged':True,'binary_sha256':after,'private_tools_accessed':False,'kiro_accessed':False}
 (STAGE/'deployment.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result),flush=True)
if __name__=='__main__':main()

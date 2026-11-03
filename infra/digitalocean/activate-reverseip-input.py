#!/usr/bin/env python3
"""Activate controller-only IPv4 preparation and UI; never run provider tools."""
import hashlib,json,os,shutil,subprocess,sys,tarfile,time,urllib.request
from pathlib import Path

release=Path(sys.argv[1]).resolve()
manifest=json.loads((release/'manifest.json').read_text())
for name,digest in manifest['sha256'].items():
 if hashlib.sha256((release/name).read_bytes()).hexdigest()!=digest:raise RuntimeError('Artifact checksum mismatch: '+name)
subprocess.run([str(release/'reverseip.test'),'-test.run=^TestReverseIP(Normalize|Bounded|AllFailed|EveryProvider|TargetValidation|Providers|Preparation|IPv6Only|RejectsMode)','-test.count=1'],cwd=release,check=True)
config_path=Path('/opt/cnc/config.json');config_bytes=config_path.read_bytes();config=json.loads(config_bytes)
base='http://'+config['http_addr'];op=urllib.request.build_opener(urllib.request.ProxyHandler({}))
def request(path):
 with op.open(base+path,timeout=8) as response:return json.load(response)
def service(action,name):subprocess.run(['systemctl',action,name],check=True,capture_output=True)
def idle():
 jobs=request('/api/jobs');jobs=jobs.get('jobs',[]) if isinstance(jobs,dict) else jobs
 if any(j['status'] in ('pending','queued','running') for j in jobs):raise RuntimeError('Active jobs present; wait rather than interrupt them')
 workers=request('/api/workers')
 if any(w['current_load'] for w in workers):raise RuntimeError('Workers are busy; deployment deferred')
 return {w['id'] for w in workers if w['status']=='online'}
expected_workers=idle()
before_auth=request('/api/config/gdrive/auth-status')
ui=release/'ui'
if ui.exists():raise RuntimeError('Release already extracted; inspect before retrying')
ui.mkdir(mode=0o700)
with tarfile.open(release/'ui.tar.gz') as archive:archive.extractall(ui,filter='data')
if not (ui/'server.js').is_file():raise RuntimeError('Missing standalone UI')
backup=release/'rollback';backup.mkdir(mode=0o700)
binary=Path('/opt/cnc/cnc-server');current_ui=Path('/opt/cnc/ui')
shutil.copy2(binary,backup/'cnc-server');shutil.copy2(config_path,backup/'config.json')
idle()
changed_ui=False
try:
 service('stop','cnc-ui');service('stop','cnc-server')
 temp=binary.with_name('cnc-server.reverseip-new');shutil.copy2(release/'cnc-server',temp);temp.chmod(0o700);os.replace(temp,binary)
 os.replace(current_ui,backup/'ui');changed_ui=True;current_ui.symlink_to(ui,target_is_directory=True)
 service('start','cnc-server');service('start','cnc-ui')
 deadline=time.monotonic()+45
 while True:
  try:
   workers=request('/api/workers')
   if not expected_workers.issubset({w['id'] for w in workers if w['status']=='online'}):raise RuntimeError('Waiting for workers')
   with op.open('http://127.0.0.1:3000/login',timeout=3) as response:assert response.status==200
   auth=request('/api/config/gdrive/auth-status')
   if before_auth.get('authorized') and not auth.get('authorized'):raise RuntimeError('Drive authorization was not retained')
   assert config_path.read_bytes()==config_bytes,'Controller configuration changed'
   break
  except Exception:
   if time.monotonic()>=deadline:raise
   time.sleep(1)
 result={'deployed':True,'workers_online':len(expected_workers),'drive_authorized':auth.get('authorized'),'config_unchanged':True,'linux_tests_passed':True,'server_sha256':manifest['sha256']['cnc-server'],'rollback':str(backup)}
 (release/'verification.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result))
except Exception:
 for unit in ('cnc-ui','cnc-server'):subprocess.run(['systemctl','stop',unit],capture_output=True)
 shutil.copy2(backup/'cnc-server',binary)
 if changed_ui:
  current_ui.unlink();os.replace(backup/'ui',current_ui)
 for unit in ('cnc-server','cnc-ui'):subprocess.run(['systemctl','start',unit],capture_output=True)
 raise

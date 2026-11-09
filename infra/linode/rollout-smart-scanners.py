#!/usr/bin/env python3
"""Stage and deploy compatible smart scanners. Explicit core files only."""
import argparse,concurrent.futures,hashlib,json,pathlib,subprocess,tarfile
ROOT=pathlib.Path(__file__).resolve().parents[2]
STAGE=pathlib.Path('/tmp/cnc-smart-release')
SERVER='cnc-linode-server'
WORKERS=[f'cnc-linode-worker-{i}' for i in range(1,7)]
SSH=['ssh','-F',str(ROOT/'.linode/ssh_config'),'-o','BatchMode=yes','-o','ConnectTimeout=15']
FILES=['cnc-api/tools.go','cnc-api/server.go','cnc-api/worker.go','cnc-api/enum.go','cnc-api/enum_worker.go','cnc-api/cms_optimization_test.go','cnc-api/worker_workspace_test.go','cnc-backend/cms-scan/cms.go','cnc-backend/cms-scan/cms_test.go','cnc-backend/cms-scan/go.mod','cnc-backend/cms-scan/go.sum','cnc-backend/enum/main.go','cnc-backend/enum/pipeline.go','cnc-backend/enum/pipeline_test.go','cnc-backend/enum/admin_response.go','cnc-backend/enum/version/scope.go','cnc-backend/enum/version/gateway.go','cnc-backend/enum/OPTIMIZATION.md','cnc-ui/components/dashboard/SubmitJobForm.tsx','cnc-ui/components/dashboard/ToolsLauncher.tsx']
FILES+=['infra/linode/rollout-smart-scanners.py','infra/linode/verify-smart-scanners.py','cnc-ui/tests/scanner-options.test.mjs']
FILES+=['cnc-backend/enum/detection/'+name for name in ['detect.go','transport.go','detect_test.go']]
def remote(host,code,**values):
 prefix=''.join(k+'='+repr(v)+'\n' for k,v in values.items())
 return subprocess.check_output(SSH+[host,'python3','-'],input=(prefix+code).encode(),timeout=180).decode()
def upload(host,path,target):
 subprocess.run(['scp','-q','-C','-F',str(ROOT/'.linode/ssh_config'),str(path),host+':'+target],check=True,timeout=180)
def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()
COMMON=r'''
import hashlib,json,pathlib,platform,shutil,sqlite3,subprocess,tarfile,time,urllib.request
release=pathlib.Path(RELEASE)
assert platform.machine()=='x86_64','Unsupported worker architecture'
def digest(p):return hashlib.sha256(pathlib.Path(p).read_bytes()).hexdigest()
def install(src,dst):
 dst=pathlib.Path(dst);assert dst.is_file() and not dst.is_symlink()
 backup=release/'rollback'/str(dst).lstrip('/');backup.parent.mkdir(parents=True,exist_ok=True)
 assert not backup.exists(),'Already activated; inspect before repeating'
 shutil.copy2(dst,backup)
 stage=dst.with_name(dst.name+'.smart-new');shutil.copy2(src,stage);stage.chmod(0o755);stage.replace(dst)
def restore(dst):
 saved=release/'rollback'/str(dst).lstrip('/');dst=pathlib.Path(dst)
 if saved.exists():
  stage=dst.with_name(dst.name+'.smart-rollback');shutil.copy2(saved,stage);stage.replace(dst)
def unpack(name):
 with tarfile.open(release/name) as t:
  for m in t.getmembers():
   assert m.isfile() and not m.issym() and not m.islnk() and not pathlib.Path(m.name).is_absolute() and '..' not in pathlib.Path(m.name).parts
  t.extractall(release,filter='data')
def workers():return json.load(urllib.request.urlopen('http://192.168.145.81:8080/api/workers',timeout=10))
settings={}
if SERVER:
 for line in pathlib.Path('/opt/cnc/processor.env').read_text().splitlines():
  k,sep,v=line.partition('=')
  if sep:settings[k]=v.strip('"')
def api(path,body=None):
 r=urllib.request.Request('http://127.0.0.1:9191/v1/'+path,headers={'Authorization':'Bearer '+settings['CNC_PROCESSOR_TOKEN'],'Content-Type':'application/json'},data=None if body is None else json.dumps(body).encode(),method='GET' if body is None else 'PUT')
 return json.load(urllib.request.urlopen(r,timeout=10))
'''
PREPARE=r'''
initial=api('health')['enabled']
api('processing-state',{'enabled':False})
try:
 w=workers();jobs=json.load(urllib.request.urlopen('http://192.168.145.81:8080/api/jobs',timeout=10))
 if isinstance(jobs,dict):jobs=jobs.get('jobs',[])
 assert len(w)==6 and all(x['status']=='online' and x['current_load']==0 for x in w),'Workers busy; release staged only'
 assert not any(j['status'] in ('running','queued','pending') for j in jobs),'Active CNC work; release staged only'
 assert api('health')['counts']['running']==0,'Processor busy'
 backup=release/'rollback';backup.mkdir(exist_ok=True)
 assert not (release/'previous-state.json').exists(),'Activation already started'
 (release/'previous-state.json').write_text(json.dumps({'enabled':initial}))
 with sqlite3.connect('/opt/cnc-processor/processor.db') as src,sqlite3.connect(backup/'processor.db') as dst:src.backup(dst)
 (backup/'processor.db').chmod(0o600)
 print(json.dumps({'prepared':True,'previous_enabled':initial}))
except Exception:
 api('processing-state',{'enabled':initial});raise
'''
ACTIVATE_WORKER=r'''
manifest=json.loads((release/'manifest.json').read_text())
assert digest(release/'worker.tar.gz')==manifest['worker_archive']
unpack('worker.tar.gz')
paths={'cnc-worker':'/opt/cnc/cnc-worker','cms-scan':'/opt/cnc/tools/cms-scan','cnc-enum':'/opt/cnc/tools/cnc-enum'}
for name in paths:assert digest(release/'bin'/name)==manifest['binaries'][name]
subprocess.run(['systemctl','stop','cnc-worker'],check=True)
try:
 for name,dst in paths.items():install(release/'bin'/name,dst)
 subprocess.run(['systemctl','start','cnc-worker'],check=True)
 subprocess.run(['systemctl','is-active','--quiet','cnc-worker'],check=True)
except Exception:
 for dst in paths.values():restore(dst)
 subprocess.run(['systemctl','start','cnc-worker'],check=False);raise
print(json.dumps({'host':HOST,'active':True,'installed':list(paths)}))
'''
ACTIVATE_SERVER=r'''
for _ in range(45):
 w=workers()
 jobs=json.load(urllib.request.urlopen('http://192.168.145.81:8080/api/jobs',timeout=10))
 if isinstance(jobs,dict):jobs=jobs.get('jobs',[])
 assert not any(j['status'] in ('running','queued','pending') for j in jobs),'New CNC work arrived; no controller service changes made'
 # Registration can temporarily report recovered terminal acknowledgements as load.
 # Wait for heartbeat/recovery reconciliation while intake is paused.
 if len(w)==6 and all(x['status']=='online' and x['current_load']==0 for x in w):break
 time.sleep(1)
else:raise RuntimeError('Worker reconnection timed out; no controller service changes made')
manifest=json.loads((release/'manifest.json').read_text())
assert digest(release/'ui.tar.gz')==manifest['ui_archive']
assert digest(release/'source.tar.gz')==manifest['source_archive']
assert digest(release/'cnc-server')==manifest['binaries']['cnc-server']
unpack('ui.tar.gz');unpack('source.tar.gz');unpack('worker.tar.gz')
config=pathlib.Path('/etc/systemd/system/cnc-ui.service.d/zz-enum-version-pipeline.conf');original=config.read_bytes()
backup=release/'rollback/ui.conf';backup.write_bytes(original);backup.chmod(0o600)
paths={'cnc-server':'/opt/cnc/cnc-server','cnc-worker':'/opt/cnc/cnc-worker-linux','cms-scan':'/opt/cnc/tools/cms-scan','cnc-enum':'/opt/cnc/tools/cnc-enum'}
processor_hash=digest('/opt/cnc-processor/cnc-processor')
try:
 subprocess.run(['systemctl','stop','cnc-server'],check=True)
 for name,dst in paths.items():install(release/('cnc-server' if name=='cnc-server' else 'bin/'+name),dst)
 entry=release/'ui/.next/standalone/server.js';assert entry.is_file()
 lines=[l for l in original.decode().splitlines() if not l.startswith(('ExecStart=','WorkingDirectory='))]
 lines+=['ExecStart=','ExecStart=/opt/cnc/node-runtime/node-v22.23.3-linux-x64/bin/node '+str(entry),'WorkingDirectory='+str(entry.parent)]
 config.write_text('\n'.join(lines)+'\n')
 subprocess.run(['systemctl','daemon-reload'],check=True)
 subprocess.run(['systemctl','start','cnc-server'],check=True)
 subprocess.run(['systemctl','restart','cnc-ui'],check=True)
 for _ in range(60):
  try:
   w=workers()
   if len(w)==6 and all(x['status']=='online' for x in w):break
  except Exception:pass
  time.sleep(1)
 else:raise RuntimeError('Workers failed to reconnect')
 assert urllib.request.urlopen('http://127.0.0.1:3000/login',timeout=8).status==200
 assert digest('/opt/cnc-processor/cnc-processor')==processor_hash
 # Synchronize only the explicit reviewed core files; keep source rollback copies.
 for name in SOURCE_FILES:
  src=release/'source'/name;dst=pathlib.Path('/opt/cnc/source')/name
  assert src.is_file() and not src.is_symlink()
  if dst.exists():
   assert dst.is_file() and not dst.is_symlink()
   saved=release/'rollback/source'/name;saved.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(dst,saved)
  dst.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(src,dst)
 print(json.dumps({'active':True,'workers_online':6,'processor_binary_unchanged':True,'rollback':str(release/'rollback'),'source_files_synced':len(SOURCE_FILES)}))
except Exception:
 for dst in paths.values():restore(dst)
 config.write_bytes(original);subprocess.run(['systemctl','daemon-reload'],check=False)
 subprocess.run(['systemctl','restart','cnc-server','cnc-ui'],check=False);raise
'''
def package():
 with tarfile.open(STAGE/'worker.tar.gz','w:gz') as t:
  for name in ['cnc-worker','cms-scan','cnc-enum']:t.add(STAGE/name,arcname='bin/'+name,recursive=False)
 with tarfile.open(STAGE/'ui.tar.gz','w:gz') as t:
  for src,prefix in [(ROOT/'cnc-ui/.next/standalone','ui/.next/standalone'),(ROOT/'cnc-ui/.next/static','ui/.next/standalone/.next/static'),(ROOT/'cnc-ui/public','ui/.next/standalone/public')]:
   for p in sorted(src.rglob('*')):
    if p.is_symlink():raise RuntimeError('Unexpected UI build symlink')
    if p.is_file():t.add(p,arcname=str(pathlib.Path(prefix)/p.relative_to(src)),recursive=False)
 with tarfile.open(STAGE/'source.tar.gz','w:gz') as t:
  for name in FILES:
   p=ROOT/name;assert p.is_file() and not p.is_symlink();t.add(p,arcname='source/'+name,recursive=False)
 manifest={'binaries':{n:sha(STAGE/n) for n in ['cnc-worker','cnc-server','cms-scan','cnc-enum']},'worker_archive':sha(STAGE/'worker.tar.gz'),'ui_archive':sha(STAGE/'ui.tar.gz'),'source_archive':sha(STAGE/'source.tar.gz')}
 release='/opt/cnc/queue-releases/smart-scanners-'+hashlib.sha256(json.dumps(manifest,sort_keys=True).encode()).hexdigest()[:16]
 manifest['release']=release;(STAGE/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n');return manifest

def main():
 parser=argparse.ArgumentParser();parser.add_argument('--stage-only',action='store_true');parser.add_argument('--activate',action='store_true');args=parser.parse_args()
 if args.activate:manifest=json.loads((STAGE/'manifest.json').read_text())
 else:
  manifest=package();release=manifest['release']
  def stage(host):
   remote(host,"from pathlib import Path\np=Path(RELEASE);p.mkdir(parents=True,exist_ok=True);p.chmod(0o700)",RELEASE=release)
   for name in ['worker.tar.gz','manifest.json']:upload(host,STAGE/name,release+'/'+name)
   if host==SERVER:
    for name in ['cnc-server','ui.tar.gz','source.tar.gz']:upload(host,STAGE/name,release+'/'+name)
  with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:list(pool.map(stage,[SERVER]+WORKERS))
  print(json.dumps({'staged':True,'release':release}),flush=True)
  if args.stage_only:return
 release=manifest['release'];values={'RELEASE':release,'SERVER':True}
 initial=json.loads(remote(SERVER,COMMON+PREPARE,**values));updated=[]
 try:
  for host in WORKERS:
   ready=json.loads(remote(SERVER,COMMON+"\nw=next(w for w in workers() if w['id']==HOST)\nprint(json.dumps(w['current_load']==0 and w['status']=='online'))",HOST=host,**values))
   assert ready,'Worker became busy; rollout cancelled'
   print(remote(host,COMMON+ACTIVATE_WORKER,HOST=host,RELEASE=release,SERVER=False),flush=True);updated.append(host)
  print(remote(SERVER,COMMON+ACTIVATE_SERVER,SOURCE_FILES=FILES,**values),flush=True)
 except Exception:
  for host in updated:
   remote(host,COMMON+"\nfor p in ['/opt/cnc/cnc-worker','/opt/cnc/tools/cms-scan','/opt/cnc/tools/cnc-enum']:restore(p)\n# Old/new protocols are compatible. Restore files but defer restart if a new task arrived.\npid=subprocess.check_output(['systemctl','show','cnc-worker','--property=MainPID','--value'],text=True).strip()\nchildren=pathlib.Path('/proc/'+pid+'/task/'+pid+'/children').read_text().strip()\nif not children:subprocess.run(['systemctl','restart','cnc-worker'],check=True)",RELEASE=release,SERVER=False)
  raise
 finally:remote(SERVER,COMMON+"\napi('processing-state',{'enabled':ENABLED})",ENABLED=initial['previous_enabled'],**values)
 result={'deployed':True,'manifest':manifest,'workers_online':6,'private_tools_accessed':False}
 (STAGE/'deployment.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result),flush=True)
if __name__=='__main__':main()

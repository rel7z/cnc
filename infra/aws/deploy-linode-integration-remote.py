import base64,gzip,hashlib,json,os,pathlib,shutil,subprocess,time,urllib.request
P=pathlib.Path
files=json.loads(gzip.decompress(base64.b64decode(SOURCE_PAYLOAD)))
digest=hashlib.sha256(json.dumps(files,sort_keys=True).encode()).hexdigest()
worker_tail=files.pop('_worker_diagnostic_tail',None)
release=P('/opt/cnc/linode-releases')/digest[:16];release.mkdir(mode=0o700,parents=True,exist_ok=True)
pid=subprocess.check_output(['systemctl','show','-p','MainPID','--value','cnc-server'],text=True).strip()
running_hash=hashlib.sha256(P('/proc',pid,'exe').read_bytes()).hexdigest()
baselines=[]
for parent in ('/opt/cnc/dashboard-releases','/opt/cnc/scheduler-releases','/opt/cnc/linode-releases'):
 if P(parent).is_dir():
  for candidate in P(parent).glob('*/cnc-server'):
   if hashlib.sha256(candidate.read_bytes()).hexdigest()==running_hash and (candidate.parent/'source/cnc-api/server.go').is_file():baselines.append(candidate.parent/'source')
if not baselines:raise RuntimeError('No verified source baseline for running controller')
base=baselines[0];source=release/'source';source.mkdir(exist_ok=True)
ignore=shutil.ignore_patterns('.next','node_modules','.git','__pycache__')
for name in ('cnc-api','cnc-backend/enum'):
 shutil.copytree(base/name,source/name,ignore=ignore,symlinks=True,dirs_exist_ok=True)
processor=source/'processor';processor.mkdir(exist_ok=True)
for name in ('go.mod','go.sum'):shutil.copy2(base/'processor'/name,processor/name)
for name in ('api','cmd','internal','cluster','deploy'):
 if (base/'processor'/name).is_dir():shutil.copytree(base/'processor'/name,processor/name,ignore=ignore,symlinks=True,dirs_exist_ok=True)
old_ui=P(subprocess.check_output(['systemctl','show','-p','WorkingDirectory','--value','cnc-ui'],text=True).strip());base_ui=old_ui.parent.parent
ui_changed=any(not (base_ui/name[len('cnc-ui/'):]).is_file() or (base_ui/name[len('cnc-ui/'):]).read_text()!=content for name,content in files.items() if name.startswith('cnc-ui/'))
shutil.copytree(base_ui,source/'cnc-ui',ignore=ignore,symlinks=True,dirs_exist_ok=True)
modules=source/'cnc-ui/node_modules'
if modules.is_symlink():
 assert modules.resolve()==(base_ui/'node_modules').resolve(),'Unexpected staged dependency path'
else:
 modules.symlink_to(base_ui/'node_modules',target_is_directory=True)
for name,content in files.items():(source/name).write_text(content)
p=source/'cnc-api/server.go';s=p.read_text();needle='linode := newLinodeIntegration(s.config.DataDir)'
assert s.count(needle)==1
if 'linode.tcpAddr, linode.httpAddr = ' not in s:s=s.replace(needle,needle+'\n\tlinode.tcpAddr, linode.httpAddr = s.config.TCPAddr, s.config.HTTPAddr')
if 'linode.prepareTools = refreshWorkerToolsPackage' not in s:s=s.replace('linode.tcpAddr, linode.httpAddr = s.config.TCPAddr, s.config.HTTPAddr','linode.tcpAddr, linode.httpAddr = s.config.TCPAddr, s.config.HTTPAddr\n\tlinode.prepareTools = refreshWorkerToolsPackage')
if 'awsCloud.prepareTools = refreshWorkerToolsPackage' not in s:s=s.replace('awsCloud := newAWSIntegration("/opt/cnc/aws-config.json")','awsCloud := newAWSIntegration("/opt/cnc/aws-config.json")\n\tawsCloud.prepareTools = refreshWorkerToolsPackage')
if 's.dorkCapacityReason(task)' not in s:
 start=s.index('func (s *Server) dispatchTask(task *Task) {')
 before=s[:start];body=s[start:]
 marker='\tvar chosen *Worker'
 hook='\tif reason := s.dorkCapacityReason(task); reason != "" {\n\t\ttask.QueueReason = reason\n\t\ts.mu.Unlock()\n\t\ts.retryCapacityTask(task)\n\t\treturn\n\t}\n\n'
 assert marker in body;body=body.replace(marker,hook+marker,1);s=before+body
if 'uploader.RequestFullSync()' not in s:
 start=s.index('func (s *Server) handleGDriveSync(')
 end=s.find('\nfunc ',start+1)
 if end<0:end=len(s)
 handler=s[start:end]
 marker='\n\tuploader.NotifyResults()\n'
 assert handler.count(marker)==1,'Manual Drive sync handler changed; refusing ambiguous patch'
 handler=handler.replace(marker,'\n\tif err := uploader.RequestFullSync(); err != nil {\n\t\thttp.Error(w, err.Error(), http.StatusInternalServerError)\n\t\treturn\n\t}\n',1)
 s=s[:start]+handler+s[end:]
p.write_text(s)
if worker_tail:
 marker='task.Error = fmt.Sprintf("exit code %d", p.Result.ExitCode)'
 assert s.count(marker)==1 or 'task.Error = taskFailureSummary(p.Result)' in s
 s=s.replace(marker,'task.Error = taskFailureSummary(p.Result)');p.write_text(s)
 wp=source/'cnc-api/worker.go';ws=wp.read_text()
 marker='log.Printf("Task %s failed (exit=%d): %s", task.ID, result.ExitCode, result.Stderr)'
 assert ws.count(marker)==1 or 'log.Printf("Task %s failed: %s", task.ID, taskFailureSummary(result))' in ws
 ws=ws.replace(marker,'log.Printf("Task %s failed: %s", task.ID, taskFailureSummary(result))')
 if '[diagnostic] Execution timed out.' not in ws:
  start=ws.index('\tstdoutStream.Close()',ws.index('func (w *WorkerAgent) executeShellTask('))
  end=ws.index('\n\tresult := &TaskResult{',start)
  ws=ws[:start]+worker_tail+'\n'+ws[end:]
 wp.write_text(ws)
g=source/'cnc-api/gdrive.go';text=g.read_text()
marker='\t\talready := u.uploaded[stateKey]\n\t\tu.uploadedMu.RUnlock()\n\t\tif !already {'
if marker in text:text=text.replace(marker,'\t\talready := u.uploaded[stateKey]\n\t\trestored := u.restored[entry.Name()] == contentHash\n\t\tu.uploadedMu.RUnlock()\n\t\tif !already && !restored {',1);g.write_text(text)
nodebin=P('/opt/cnc/node-path').read_text().strip()+'/bin';config=json.loads(P('/opt/cnc/config.json').read_text())
env=dict(os.environ,PATH=nodebin+':'+os.environ['PATH'],GOPATH='/opt/cnc/go-work',GOMODCACHE='/opt/cnc/go-work/pkg/mod',GOCACHE='/opt/cnc/go-cache',GO_API_URL='http://'+config['http_addr'],NEXT_TELEMETRY_DISABLED='1')
commands=[('cnc-api',['/opt/cnc/go/bin/go','test','-race','.','-run','TestLinode|TestAWS|TestDorkCapacity|TestDrive|TestGDrive|TestTaskFailure|TestWorkerExecuteTaskToolsResolution|TestShell','-count=1']),('cnc-api',['/opt/cnc/go/bin/go','build','-trimpath','-ldflags=-s -w','-o',str(release/'cnc-server'),'./cmd/server']),('cnc-ui',[nodebin+'/npm','run','build','--','--webpack'])]
if worker_tail:commands.append(('cnc-api',['/opt/cnc/go/bin/go','build','-trimpath','-ldflags=-s -w','-o',str(release/'cnc-worker-linux'),'./cmd/worker']))
if not ui_changed:commands=[c for c in commands if c[0]!='cnc-ui']
with (release/'build.log').open('w') as log:
 for directory,command in commands:subprocess.run(command,cwd=source/directory,env=dict(env,CGO_ENABLED='1' if 'test' in command else '0'),stdout=log,stderr=subprocess.STDOUT,check=True,timeout=600)
ui=source/'cnc-ui';standalone=ui/'.next/standalone' if ui_changed else old_ui
if ui_changed:
 shutil.copytree(ui/'.next/static',standalone/'.next/static',dirs_exist_ok=True)
 if (ui/'public').is_dir():shutil.copytree(ui/'public',standalone/'public',dirs_exist_ok=True)
# No controller restart while any work is active.
o=urllib.request.build_opener(urllib.request.ProxyHandler({}))
def get(url,headers=None):
 with o.open(urllib.request.Request(url,headers=headers or {}),timeout=15) as r:return json.load(r)
v={}
for line in P('/opt/cnc/processor.env').read_text().splitlines():
 if line and not line.startswith('#') and '=' in line:k,x=line.split('=',1);v[k]=x.strip('"')
health=get('http://127.0.0.1:9191/v1/health',{'Authorization':'Bearer '+v['CNC_PROCESSOR_TOKEN']})
assert health['counts']['running']==0,'Active processor jobs; release staged without activation'
assert not any(j.get('status') in ('pending','running') for j in get('http://'+config['http_addr']+'/api/jobs')),'Active controller jobs; release staged without activation'
target=P('/opt/cnc/cnc-server');shutil.copy2(target,release/'rollback-cnc-server')
worker_target=P('/opt/cnc/cnc-worker-linux')
if worker_tail:shutil.copy2(worker_target,release/'rollback-cnc-worker-linux')
drop=P('/etc/systemd/system/cnc-ui.service.d/zzzzz-dashboard-recovery.conf');previous=drop.read_bytes();(release/'rollback-ui.conf').write_bytes(previous)
try:
 new=target.with_suffix('.new');shutil.copy2(release/'cnc-server',new);new.chmod(0o700);os.replace(new,target)
 if worker_tail:
  new=worker_target.with_suffix('.new');shutil.copy2(release/'cnc-worker-linux',new);new.chmod(0o700);os.replace(new,worker_target)
 subprocess.run(['systemctl','restart','cnc-server'],check=True,timeout=60)
 deadline=time.monotonic()+40
 while True:
  try:detected=get('http://'+config['http_addr']+'/api/linode/connection');assert detected.get('network')=='WireGuard';break
  except OSError:
   if time.monotonic()>deadline:raise
   time.sleep(1)
 if ui_changed:drop.write_text('[Service]\nTimeoutStopSec=10\nWorkingDirectory='+str(standalone)+'\nExecStart=\nExecStart='+nodebin+'/node '+str(standalone/'server.js')+'\n')
 if ui_changed:
  subprocess.run(['systemctl','daemon-reload'],check=True);subprocess.run(['systemctl','restart','cnc-ui'],check=True,timeout=60)
 deadline=time.monotonic()+40
 while True:
  try:
   with o.open('http://127.0.0.1:3000/login',timeout=5) as r:assert r.status==200
   break
  except OSError:
   if time.monotonic()>deadline:raise
   time.sleep(1)
except BaseException:
 if worker_tail:shutil.copy2(release/'rollback-cnc-worker-linux',worker_target)
 shutil.copy2(release/'rollback-cnc-server',target);drop.write_bytes(previous);subprocess.run(['systemctl','daemon-reload'],check=True)
 for svc in ('cnc-server','cnc-ui'):subprocess.run(['systemctl','restart',svc],check=True,timeout=60)
 raise
print(json.dumps({'deployed':True,'release':str(release),'linode_tests_and_ui_build_passed':True,'private_connection':detected,'processor_state_unchanged':True,'tools_accessed':False,'worker_binary_sha256':hashlib.sha256(worker_target.read_bytes()).hexdigest() if worker_tail else None}))

import base64,gzip,hashlib,json,os,pathlib,shutil,subprocess,time,urllib.request
P=pathlib.Path
payload=json.loads(gzip.decompress(base64.b64decode(SOURCE_PAYLOAD)))
files=payload['files'];digest=hashlib.sha256(json.dumps({'files':files,'block':payload['registry_block']},sort_keys=True).encode()).hexdigest()
root=P('/opt/cnc');release=root/'dnsrift-releases'/digest[:16];release.mkdir(parents=True,exist_ok=True,mode=0o700)
config=json.loads((root/'config.json').read_text());op=urllib.request.build_opener(urllib.request.ProxyHandler({}));base='http://'+config['http_addr']
def api(path):
 with op.open(base+path,timeout=20) as r:return json.load(r)
def running_hash():
 pid=subprocess.check_output(['systemctl','show','-p','MainPID','--value','cnc-server'],text=True).strip()
 return hashlib.sha256(P('/proc',pid,'exe').read_bytes()).hexdigest()
if not (release/'STAGED.json').exists():
 current_hash=running_hash();candidates=[]
 for parent in ('dashboard-releases','scheduler-releases','linode-releases','dnsrift-releases'):
  for binary in (root/parent).glob('*/cnc-server'):
   if hashlib.sha256(binary.read_bytes()).hexdigest()==current_hash and (binary.parent/'source/cnc-api/server.go').exists():candidates.append(binary.parent/'source')
 assert candidates,'No source baseline matching the running controller'
 baseline=candidates[0];source=release/'source';source.mkdir(exist_ok=True)
 ignore=shutil.ignore_patterns('.next','node_modules','.git','__pycache__')
 for name in ('cnc-api','cnc-backend/enum'):
  shutil.copytree(baseline/name,source/name,ignore=ignore,symlinks=True,dirs_exist_ok=True)
 processor=source/'processor';processor.mkdir(exist_ok=True)
 for name in ('go.mod','go.sum'):shutil.copy2(baseline/'processor'/name,processor/name)
 for name in ('api','cmd','internal','cluster','deploy'):
  if (baseline/'processor'/name).is_dir():shutil.copytree(baseline/'processor'/name,processor/name,ignore=ignore,symlinks=True,dirs_exist_ok=True)
 old_ui=P(subprocess.check_output(['systemctl','show','-p','WorkingDirectory','--value','cnc-ui'],text=True).strip());base_ui=old_ui.parent.parent
 shutil.copytree(base_ui,source/'cnc-ui',ignore=ignore,symlinks=True,dirs_exist_ok=True)
 modules=source/'cnc-ui/node_modules'
 if not modules.is_symlink():modules.symlink_to(base_ui/'node_modules',target_is_directory=True)
 for name,content in files.items():
  path=source/name;path.parent.mkdir(parents=True,exist_ok=True);path.write_text(content)
 registry=source/'cnc-api/tools.go';text=registry.read_text()
 if '"reverseip-dnsrift": {' not in text:
  marker='\t"reverseip-thc": {';assert text.count(marker)==1;text=text.replace(marker,payload['registry_block']+'\n'+marker,1)
 start=text.index('\t"reverseip-domain": {');end=text.index('\n\t"subnet-finder": {',start);block=text[start:end]
 assert 'Scope:' in block
 import re
 block=re.sub(r'(Scope:\s*)"server"',r'\1"worker"',block)
 if 'DefaultMode:' not in block:block=block.replace('"worker",','"worker",\n\t\tDefaultMode: JobModeSpread,',1)
 block=block.replace('Server-side execution.','Load balances input across worker servers.')
 text=text[:start]+block+text[end:];registry.write_text(text)
 test=source/'cnc-api/server_tool_test.go'
 if test.exists():
  t=test.read_text().replace('domain.Scope != "server"','domain.Scope != "worker"').replace("reverseip-domain scope to be 'server'","reverseip-domain scope to be 'worker'");test.write_text(t)
 env=dict(os.environ,GOPATH=str(root/'go-work'),GOMODCACHE=str(root/'go-work/pkg/mod'),GOCACHE=str(root/'go-cache'))
 go=str(root/'go/bin/go');nodebin=(root/'node-path').read_text().strip()+'/bin';env.update(PATH=nodebin+':'+env['PATH'],GO_API_URL=base,NEXT_TELEMETRY_DISABLED='1')
 commands=[('cnc-api',[go,'test','-race','.','-run','TestReverseIPProviders|TestToolsRegistryServerTools|TestGenerateToolCommandServerTools|TestToolsLaunch','-count=1']),('cnc-backend/reverseip/src3',[go,'test','-race','./...']),('cnc-backend/reverseip/src3',[go,'build','-trimpath','-ldflags=-s -w','-o',str(release/'reverseip-dnsrift'),'.']),('cnc-api',[go,'build','-trimpath','-ldflags=-s -w','-o',str(release/'cnc-server'),'./cmd/server']),('cnc-ui',[nodebin+'/npm','run','build','--','--webpack'])]
 with (release/'build.log').open('w') as log:
  for directory,cmd in commands:subprocess.run(cmd,cwd=source/directory,env=dict(env,CGO_ENABLED='1' if 'test' in cmd else '0'),stdout=log,stderr=subprocess.STDOUT,check=True,timeout=600)
 ui=source/'cnc-ui';standalone=ui/'.next/standalone';shutil.copytree(ui/'.next/static',standalone/'.next/static',dirs_exist_ok=True)
 if (ui/'public').is_dir():shutil.copytree(ui/'public',standalone/'public',dirs_exist_ok=True)
 stage={'release':str(release),'source_payload_sha256':digest,'previous_controller_sha256':current_hash,'binary_sha256':hashlib.sha256((release/'reverseip-dnsrift').read_bytes()).hexdigest(),'git_revision':payload['git_revision'],'standalone':str(standalone),'nodebin':nodebin}
 (release/'STAGED.json').write_text(json.dumps(stage,indent=2))
else:stage=json.loads((release/'STAGED.json').read_text())
# New binary only: existing tool binaries and worker processes are untouched.
target=root/'tools/reverseip-dnsrift'
if target.exists() and not (release/'rollback-reverseip-dnsrift').exists():shutil.copy2(target,release/'rollback-reverseip-dnsrift')
temp=target.with_suffix('.new');shutil.copy2(release/'reverseip-dnsrift',temp);temp.chmod(0o700);os.replace(temp,target)
check=subprocess.run([str(target),'--help'],capture_output=True,text=True,timeout=10);assert check.returncode==0 and '-ip' in check.stderr
exec(compile(files['cnc-api/worker_tools_package.py'],'generic-package-build','exec'),{'__name__':'__main__'})
active=[j['id'] for j in api('/api/jobs') if j.get('status') in ('pending','running')]
deployed=(release/'DEPLOYED').exists() and running_hash()==hashlib.sha256((release/'cnc-server').read_bytes()).hexdigest()
if payload['activate'] and not active and not deployed:
 assert running_hash()==stage['previous_controller_sha256'],'Controller changed since staging; inspect before activation'
 source=release/'source';target=root/'cnc-server';shutil.copy2(target,release/'rollback-cnc-server')
 drop=P('/etc/systemd/system/cnc-ui.service.d/zzzzz-dashboard-recovery.conf');previous=drop.read_bytes();(release/'rollback-ui.conf').write_bytes(previous)
 try:
  temp=target.with_suffix('.new');shutil.copy2(release/'cnc-server',temp);temp.chmod(0o700);os.replace(temp,target)
  subprocess.run(['systemctl','restart','cnc-server'],check=True,timeout=60)
  deadline=time.monotonic()+45
  while True:
   try:
    tools=api('/api/tools');providers=[t for t in tools if t['id'].startswith('reverseip-')]
    assert len(providers)==3 and all(t.get('scope')=='worker' and t.get('default_mode')=='spread' for t in providers);break
   except (OSError,AssertionError):
    if time.monotonic()>deadline:raise
    time.sleep(1)
  drop.write_text('[Service]\nTimeoutStopSec=10\nWorkingDirectory='+stage['standalone']+'\nExecStart=\nExecStart='+stage['nodebin']+'/node '+stage['standalone']+'/server.js\n')
  subprocess.run(['systemctl','daemon-reload'],check=True);subprocess.run(['systemctl','restart','cnc-ui'],check=True,timeout=60)
  assert subprocess.check_output(['systemctl','show','-p','WorkingDirectory','--value','cnc-ui'],text=True).strip()==stage['standalone'],'UI did not activate the staged grouped launcher'
  deadline=time.monotonic()+30
  while True:
   try:
    with op.open('http://127.0.0.1:3000/login',timeout=5) as r:assert r.status==200
    break
   except OSError:
    if time.monotonic()>deadline:raise
    time.sleep(1)
  deployed=True;(release/'DEPLOYED').write_text(payload['git_revision']+'\n')
 except BaseException:
  shutil.copy2(release/'rollback-cnc-server',target);drop.write_bytes(previous);subprocess.run(['systemctl','daemon-reload'],check=True)
  for svc in ('cnc-server','cnc-ui'):subprocess.run(['systemctl','restart',svc],check=True,timeout=60)
  raise
print(json.dumps(dict(stage,staged=True,deployed=deployed,active_jobs=active,restricted_processor_tools_accessed=False,worker_services_restarted=False)))

#!/usr/bin/env python3
"""Install a new CNC controller from reviewed binaries; no private tool access."""
import hashlib,json,os,shutil,subprocess,tarfile,urllib.request,time
from pathlib import Path
from runtime import install_node
STAGE=Path('/opt/cnc-deploy');APP=Path('/opt/cnc');PROC=Path('/opt/cnc-processor')
manifest=json.loads((STAGE/'manifest.json').read_text());settings=json.loads((STAGE/'deploy-config.json').read_text())
if (APP/'config.json').exists():raise RuntimeError('Controller is already configured; refusing an initial-install overwrite')
if hashlib.sha256((STAGE/'artifacts.tar.gz').read_bytes()).hexdigest()!=manifest['sha256']['artifacts.tar.gz']:raise RuntimeError('Artifact checksum mismatch')
for p in [APP,APP/'data',APP/'merged',PROC]:p.mkdir(parents=True,mode=0o700,exist_ok=True)
with tarfile.open(STAGE/'artifacts.tar.gz') as tar:
 for item in tar.getmembers():
  if not item.isfile() or Path(item.name).is_absolute() or '..' in Path(item.name).parts:raise RuntimeError('Unsafe artifact archive')
 tar.extractall(STAGE/'release',filter='data')
release=STAGE/'release'
(APP/'recovery').mkdir(mode=0o700,exist_ok=True)
if hashlib.sha256((release/'recovery/internet_map_recovery.py').read_bytes()).hexdigest()!=manifest['sha256']['recovery/internet_map_recovery.py']:raise RuntimeError('Recovery helper checksum mismatch')
shutil.copy2(release/'recovery/internet_map_recovery.py',APP/'recovery/internet_map_recovery.py')
for name,target in [('cnc-server',APP/'cnc-server'),('cnc-worker',APP/'cnc-worker-linux'),('cnc-processor',PROC/'cnc-processor')]:
 source=release/'bin'/name
 if hashlib.sha256(source.read_bytes()).hexdigest()!=manifest['sha256']['bin/'+name]:raise RuntimeError('Binary checksum mismatch')
 shutil.copy2(source,target);target.chmod(0o700)
shutil.copytree(release/'tools',APP/'tools',dirs_exist_ok=True)
for path in (APP/'tools').iterdir():
 if path.is_file():path.chmod(0o755)
node=install_node();env=dict(os.environ,PATH=str(node/'bin')+':'+os.environ['PATH'],NEXT_TELEMETRY_DISABLED='1')
log=(STAGE/'install.log').open('a')
def run(args,**kwargs):subprocess.run(args,check=True,stdout=log,stderr=subprocess.STDOUT,**kwargs)
run([str(node/'bin/npm'),'ci','--no-audit','--no-fund'],cwd=APP/'tools/dork2-src',env=env)
(APP/'tools/dork2').write_text('#!/bin/sh\nexec "$(cat /opt/cnc/node-path)/bin/node" /opt/cnc/tools/dork2-src/index.js "$@"\n');(APP/'tools/dork2').chmod(0o755)
run(['python3',str(release/'worker_tools_package.py')])
# Build the dashboard from source with the controller's private backend address.
with tarfile.open(release/'ui-source.tar.gz') as tar:
 for item in tar.getmembers():
  if not item.isfile() or not item.name.startswith('ui/') or '..' in Path(item.name).parts:raise RuntimeError('Unsafe UI source')
 tar.extractall(STAGE,filter='data')
private=settings['controller_private_ip'];env['GO_API_URL']='http://'+private+':8080'
run([str(node/'bin/npm'),'ci','--no-audit','--no-fund'],cwd=STAGE/'ui',env=env)
run([str(node/'bin/npm'),'run','build'],cwd=STAGE/'ui',env=env)
shutil.copytree(STAGE/'ui/.next/standalone',APP/'ui',symlinks=True)
shutil.copytree(STAGE/'ui/.next/static',APP/'ui/.next/static',dirs_exist_ok=True)
shutil.copytree(STAGE/'ui/public',APP/'ui/public',dirs_exist_ok=True)
config={'http_addr':private+':8080','tcp_addr':private+':9090','data_dir':str(APP/'data'),'local_admin_only':True,'gdrive':{'enabled':False,'watch_dir':str(APP/'merged'),'upload_interval':'1s'},'telegram':{'enabled':False}}
(APP/'config.json').write_text(json.dumps(config,indent=2)+'\n');(APP/'config.json').chmod(0o600)
(PROC/'config.json').write_text(json.dumps({'execution_mode':'queue','routes':{},'programs':{}})+'\n');(PROC/'config.json').chmod(0o600)
login=settings['login'];(APP/'login.env').write_text('CNC_LOGIN_USERNAME='+login['username']+'\nCNC_LOGIN_PASSWORD='+login['password']+'\nCNC_SESSION_SECRET='+login['session_secret']+'\n');(APP/'login.env').chmod(0o600)
(APP/'dashboard-audit.env').write_text('CNC_DASHBOARD_AUDIT_SECRET='+settings['audit_secret']+'\n');(APP/'dashboard-audit.env').chmod(0o600)
penv={'CNC_PROCESSOR_ENABLED':'1','CNC_PROCESSOR_TOKEN':settings['processor_token'],'CNC_APP_DIR':str(PROC),'CNC_PROCESSOR_DB':str(PROC/'processor.db'),'CNC_WATCH_DIR':str(APP/'merged'),'CNC_PROCESSOR_BROKER_URL':'http://'+private+':8080','CNC_PROCESSOR_WORKER_URL':'http://'+private+':8080/api/processor'}
(APP/'processor.env').write_text(''.join(k+'='+v+'\n' for k,v in penv.items()));(APP/'processor.env').chmod(0o600)
if settings.get('worker_password'):
 (APP/'data/worker-default-password').write_text(settings['worker_password']);(APP/'data/worker-default-password').chmod(0o600)
 store=APP/'data/worker-logins';store.mkdir(mode=0o700)
 for name in settings['worker_names']:(store/name).write_text(settings['worker_password']);(store/name).chmod(0o600)
(APP/'RELEASE').write_text(manifest['release']+'\n')
units=Path('/etc/systemd/system')
(units/'internet-map-recovery.service').write_text('[Unit]\nAfter=network-online.target cnc-server.service\nWants=network-online.target\n[Service]\nType=oneshot\nUser=root\nUMask=0077\nExecStart=/usr/bin/python3 /opt/cnc/recovery/internet_map_recovery.py --install\nTimeoutStartSec=1h\n')
common='[Unit]\nAfter=network-online.target\nWants=network-online.target\n[Service]\nUser=root\nRestart=always\nRestartSec=5\nOOMPolicy=stop\nUMask=0077\n'
(units/'cnc-server.service').write_text(common+'WorkingDirectory=/opt/cnc\nEnvironmentFile=/opt/cnc/processor.env\nEnvironmentFile=/opt/cnc/dashboard-audit.env\nExecStart=/opt/cnc/cnc-server -config=/opt/cnc/config.json\n[Install]\nWantedBy=multi-user.target\n')
(units/'cnc-processor.service').write_text(common+'WorkingDirectory=/opt/cnc-processor\nEnvironmentFile=/opt/cnc/processor.env\nExecStart=/opt/cnc-processor/cnc-processor\nTimeoutStopSec=60\n[Install]\nWantedBy=multi-user.target\n')
(units/'cnc-ui.service').write_text(common+'WorkingDirectory=/opt/cnc/ui\nEnvironment=HOSTNAME=127.0.0.1\nEnvironment=PORT=3000\nEnvironment=GO_API_URL=http://'+private+':8080\nEnvironmentFile=/opt/cnc/login.env\nEnvironmentFile=/opt/cnc/processor.env\nEnvironmentFile=/opt/cnc/dashboard-audit.env\nExecStart='+str(node/'bin/node')+' /opt/cnc/ui/server.js\n[Install]\nWantedBy=multi-user.target\n')
run(['systemctl','daemon-reload']);run(['systemctl','enable','--now','cnc-server','cnc-processor','cnc-ui'])
# Fresh processor state starts disabled; verify rather than activating it.
op=urllib.request.build_opener(urllib.request.ProxyHandler({}));deadline=time.monotonic()+60
while True:
 try:
  request=urllib.request.Request('http://127.0.0.1:9191/v1/health',headers={'Authorization':'Bearer '+settings['processor_token']})
  with op.open(request,timeout=5) as response:health=json.load(response)
  if health['enabled']:raise RuntimeError('Fresh processor unexpectedly enabled')
  with op.open('http://127.0.0.1:3000/login',timeout=5) as response:
   if response.status!=200:raise RuntimeError('Dashboard not ready')
  break
 except (OSError,urllib.error.URLError):
  if time.monotonic()>deadline:raise
  time.sleep(1)
receipt={'controller_installed':True,'dashboard_http_status':200,'processor_paused':not health['enabled'],'private_tools_accessed':False,'private_programs_configured':0,'release':manifest['release']}
(STAGE/'controller-installed.json').write_text(json.dumps(receipt,indent=2)+'\n');log.close();print(json.dumps(receipt),flush=True)
(STAGE/'deploy-config.json').unlink()

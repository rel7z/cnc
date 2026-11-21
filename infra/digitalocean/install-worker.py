#!/usr/bin/env python3
"""Install one new CNC worker; start no scans and copy no private programs."""
import hashlib,json,os,shutil,subprocess,tarfile,time,urllib.request
from pathlib import Path
from runtime import install_node
from worker_login_policy import password_setup
ROOT=Path('/opt/cnc');STAGE=Path('/opt/cnc-deploy');settings=json.loads((STAGE/'worker-config.json').read_text())
if (ROOT/'config.json').exists():raise RuntimeError('Worker already configured; refusing an initial-install overwrite')
ROOT.mkdir(parents=True,mode=0o700,exist_ok=True);(ROOT/'data').mkdir(mode=0o700,exist_ok=True)
Path('/root/processor').mkdir(mode=0o700,exist_ok=True)
node=install_node();op=urllib.request.build_opener(urllib.request.ProxyHandler({}));base='http://'+settings['controller_private_ip']+':8080'
for attempt in range(120):
 try:
  for endpoint,name in [('cnc-worker-linux','cnc-worker.new'),('cnc-worker-tools','tools.tar.gz')]:
   with op.open(base+'/download/'+endpoint,timeout=30) as response:(ROOT/name).write_bytes(response.read())
  break
 except OSError:
  if attempt==119:raise
  time.sleep(3)
if hashlib.sha256((ROOT/'cnc-worker.new').read_bytes()).hexdigest()!=settings['worker_sha256']:raise RuntimeError('Worker checksum mismatch')
(ROOT/'cnc-worker.new').chmod(0o700);(ROOT/'cnc-worker.new').replace(ROOT/'cnc-worker')
with tarfile.open(ROOT/'tools.tar.gz') as tar:
 for item in tar.getmembers():
  if not item.isfile() or not item.name.startswith('tools/') or '..' in Path(item.name).parts:raise RuntimeError('Unsafe generic tools archive')
 tar.extractall(ROOT,filter='data')
subprocess.run(['python3',str(STAGE/'worker_tools_manifest.py')],check=True)
config={'server_addr':settings['controller_private_ip']+':9090','worker_id':settings['name'],'max_tasks':8,'data_dir':str(ROOT/'data'),'processor_workspace_root':'/root/processor','ssh_host':settings['public_ip'],'ssh_user':'root','ssh_port':22}
config['spread_workspace_root'] = '/root'
(ROOT/'config.json').write_text(json.dumps(config,indent=2)+'\n');(ROOT/'config.json').chmod(0o600)
if settings.get('password'):
 subprocess.run(['bash','-se'],input=password_setup(settings['password']),text=True,check=True)
(ROOT/'cnc-worker').chmod(0o700);shutil.copy2(ROOT/'cnc-worker',ROOT/'worker-stop-guard')
subprocess.run([str(ROOT/'cnc-worker'),'-drain-support'],check=True,stdout=subprocess.DEVNULL)
unit='''[Unit]
Description=CNC worker
After=network-online.target
Wants=network-online.target
[Service]
User=root
WorkingDirectory=/opt/cnc
Environment=PATH=NODEBIN:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
Environment=ENUM_MAX_INFLIGHT_REQUESTS=256
Environment=CNC_ENUM_CHECKPOINT=1
Environment=CNC_WORDPRESS_BULK=1
ExecStart=/opt/cnc/cnc-worker -config=/opt/cnc/config.json
ExecStop=/opt/cnc/worker-stop-guard -config=/opt/cnc/config.json -drain-stop $MAINPID
TimeoutStopSec=infinity
KillMode=mixed
Restart=always
RestartSec=5
OOMPolicy=stop
UMask=0077
[Install]
WantedBy=multi-user.target
'''.replace('NODEBIN',str(node/'bin'))
Path('/etc/systemd/system/cnc-worker.service').write_text(unit)
subprocess.run(['systemctl','daemon-reload'],check=True);subprocess.run(['systemctl','enable','--now','cnc-worker'],check=True);subprocess.run(['systemctl','is-active','--quiet','cnc-worker'],check=True)
receipt={'worker':settings['name'],'worker_sha256':hashlib.sha256((ROOT/'cnc-worker').read_bytes()).hexdigest(),'scanner_sha256':hashlib.sha256((ROOT/'tools/cnc-enum').read_bytes()).hexdigest(),'private_tools_accessed':False,'service':'active','root_password_policy_configured':bool(settings.get('password'))}
(STAGE/'worker-installed.json').write_text(json.dumps(receipt,indent=2)+'\n');print(json.dumps(receipt),flush=True)
(STAGE/'worker-config.json').unlink()

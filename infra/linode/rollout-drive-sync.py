#!/usr/bin/env python3
"""Deploy Drive deletion reconciliation and the corrected reconnect guidance."""
import hashlib
import gzip
import json
from pathlib import Path
import shutil
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[2]
HOST = 'cnc-linode-server'
BINARY = Path('/tmp/cnc-server-drive-sync')
BINARY_GZ = Path('/tmp/cnc-server-drive-sync.gz')
UI_TAR = Path('/tmp/cnc-drive-sync-ui.tar.gz')
SOURCE_TAR = Path('/tmp/cnc-drive-sync-source.tar.gz')
SSH_CONFIG = ROOT / '.linode/ssh_config'


def remote(code, **values):
    prefix = ''.join(key + '=' + repr(value) + '\n' for key, value in values.items())
    return subprocess.check_output(
        ['ssh', '-F', str(SSH_CONFIG), '-o', 'BatchMode=yes', HOST, 'python3', '-'],
        input=(prefix + code).encode(), text=False,
    ).decode()


def upload(path, destination):
    subprocess.run([
        'scp', '-q', '-C', '-F', str(SSH_CONFIG),
        '-o', 'ConnectTimeout=20', '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=4',
        str(path), HOST + ':' + destination,
    ], check=True, timeout=240)


def build_ui_archive():
    paths = [
        (ROOT / 'cnc-ui/.next/standalone', 'ui/.next/standalone'),
        (ROOT / 'cnc-ui/.next/static', 'ui/.next/standalone/.next/static'),
        (ROOT / 'cnc-ui/public', 'ui/.next/standalone/public'),
    ]
    with tarfile.open(UI_TAR, 'w:gz') as archive:
        for source, prefix in paths:
            if not source.is_dir():
                raise RuntimeError('Missing production UI build output: ' + str(source))
            for path in sorted(source.rglob('*')):
                if path.is_symlink():
                    raise RuntimeError('Unexpected symlink in UI build: ' + str(path))
                if path.is_file():
                    archive.add(path, arcname=str(Path(prefix) / path.relative_to(source)), recursive=False)


def build_source_archive():
    files = [
        (ROOT / 'cnc-api/gdrive.go', 'cnc-api/gdrive.go'),
        (ROOT / 'cnc-api/gdrive_sync_test.go', 'cnc-api/gdrive_sync_test.go'),
        (ROOT / 'cnc-api/gdrive_settings.go', 'cnc-api/gdrive_settings.go'),
        (ROOT / 'cnc-api/gdrive_settings_test.go', 'cnc-api/gdrive_settings_test.go'),
        (ROOT / 'cnc-api/server.go', 'cnc-api/server.go'),
        (ROOT / 'cnc-api/GDRIVE_SETUP.md', 'cnc-api/GDRIVE_SETUP.md'),
        (ROOT / 'cnc-ui/components/dashboard/GDriveSettings.tsx', 'cnc-ui/components/dashboard/GDriveSettings.tsx'),
        (ROOT / 'cnc-ui/app/drive-connect/route.ts', 'cnc-ui/app/drive-connect/route.ts'),
        (ROOT / 'cnc-ui/lib/drive-connect.ts', 'cnc-ui/lib/drive-connect.ts'),
        (ROOT / 'cnc-ui/proxy.ts', 'cnc-ui/proxy.ts'),
        (ROOT / 'cnc-ui/tests/gdrive-connect.test.mjs', 'cnc-ui/tests/gdrive-connect.test.mjs'),
        (ROOT / 'infra/linode/connect-drive-tunnel.sh', 'infra/linode/connect-drive-tunnel.sh'),
        (ROOT / 'infra/linode/rollout-drive-sync.py', 'infra/linode/rollout-drive-sync.py'),
    ]
    with tarfile.open(SOURCE_TAR, 'w:gz') as archive:
        for source, name in files:
            if source.is_symlink() or not source.is_file():
                raise RuntimeError('Missing or symlinked deployment source: ' + str(source))
            archive.add(source, arcname=name, recursive=False)


ACTIVATE = r'''
import hashlib,json,os,shutil,subprocess,tarfile,time,urllib.request
from pathlib import Path
release=Path(RELEASE)
server=Path('/opt/cnc/cnc-server')
source=Path('/tmp/cnc-server-drive-sync')
ui_archive=Path('/tmp/cnc-drive-sync-ui.tar.gz')
assert hashlib.sha256(source.read_bytes()).hexdigest()==SERVER_SHA
assert hashlib.sha256(ui_archive.read_bytes()).hexdigest()==UI_SHA
assert server.is_file() and not server.is_symlink()
assert subprocess.check_output(['systemctl','is-active','cnc-server.service'],text=True).strip()=='active'
assert subprocess.check_output(['systemctl','is-active','cnc-ui.service'],text=True).strip()=='active'
workers=json.load(urllib.request.urlopen('http://192.168.145.81:8080/api/workers',timeout=8))
assert sum(1 for w in workers if w.get('status')=='online')==6
jobs=json.load(urllib.request.urlopen('http://192.168.145.81:8080/api/jobs',timeout=8))
if isinstance(jobs,dict):jobs=jobs.get('jobs',[])
assert not any(j.get('status') in ('queued','running','pending') for j in jobs),'Controller has active jobs'
rollback=release/'rollback';rollback.mkdir(parents=True,exist_ok=True);rollback.chmod(0o700)
server_backup=rollback/'cnc-server'
if not server_backup.exists():shutil.copy2(server,server_backup)
server_backup.chmod(0o700)
dropin=Path('/etc/systemd/system/cnc-ui.service.d/zz-enum-version-pipeline.conf')
original=dropin.read_bytes();(rollback/'cnc-ui-dropin.conf').write_bytes(original);(rollback/'cnc-ui-dropin.conf').chmod(0o600)
with tarfile.open(ui_archive) as archive:
 assert all((m.isfile() or m.isdir()) and m.name.startswith('ui/') for m in archive.getmembers())
 archive.extractall(release,filter='data')
ui_entry=release/'ui/.next/standalone/server.js'
assert ui_entry.is_file()
pid=int(subprocess.check_output(['systemctl','show','cnc-ui.service','--property=MainPID','--value'],text=True).strip())
node=Path(os.readlink(f'/proc/{pid}/exe'))
assert node.name=='node',f'Unexpected Node runtime {node}'
lines=original.decode().splitlines()
lines=[line for line in lines if not line.startswith(('ExecStart=','WorkingDirectory=','Environment=GO_API_URL='))]
lines+=['ExecStart=','ExecStart='+str(node)+' '+str(ui_entry),'WorkingDirectory='+str(ui_entry.parent),'Environment=GO_API_URL=http://192.168.145.81:8080']
temp=dropin.with_name(dropin.name+'.new');temp.write_text('\n'.join(lines)+'\n');temp.chmod(dropin.stat().st_mode&0o777)

try:
 stage=server.with_name('cnc-server.drive-sync-new');shutil.copy2(source,stage);stage.chmod(0o755);stage.replace(server)
 assert hashlib.sha256(server.read_bytes()).hexdigest()==SERVER_SHA
 temp.replace(dropin)
 subprocess.run(['systemctl','daemon-reload'],check=True)
 subprocess.run(['systemctl','restart','cnc-server.service'],check=True)
 subprocess.run(['systemctl','restart','cnc-ui.service'],check=True)
 for attempt in range(60):
  try:
   assert subprocess.check_output(['systemctl','is-active','cnc-server.service'],text=True).strip()=='active'
   assert subprocess.check_output(['systemctl','is-active','cnc-ui.service'],text=True).strip()=='active'
   with urllib.request.urlopen('http://127.0.0.1:3000/login',timeout=5) as response:assert response.status==200
   with urllib.request.urlopen('http://192.168.145.81:8080/api/config/gdrive/auth-status',timeout=5) as response:
    auth=json.load(response)
   with urllib.request.urlopen('http://192.168.145.81:8080/api/workers',timeout=5) as response:workers=json.load(response)
   if sum(1 for w in workers if w.get('status')=='online')==6:break
  except Exception:
   if attempt==59:raise
  if attempt==59:raise RuntimeError('Workers did not reconnect after controller restart')
  time.sleep(1)
 else:raise RuntimeError('Workers did not reconnect after controller restart')
 assert auth.get('configured') is True and auth.get('authorized') is False
 assert hashlib.sha256(server.read_bytes()).hexdigest()==SERVER_SHA
 print(json.dumps({'server_sha256':SERVER_SHA,'ui_sha256':UI_SHA,'server_active':True,'ui_active':True,'dashboard_login_http':200,'workers_online':6,'drive_configured':True,'drive_needs_reauthorization':True,'active_jobs_before_rollout':0,'rollback_server':str(server_backup),'rollback_ui_dropin':str(rollback/'cnc-ui-dropin.conf'),'services_restarted':['cnc-server.service','cnc-ui.service']}))
except Exception:
 rollback_stage=server.with_name('cnc-server.rollback-new');shutil.copy2(server_backup,rollback_stage);rollback_stage.chmod(0o755);rollback_stage.replace(server)
 dropin.write_bytes(original)
 subprocess.run(['systemctl','daemon-reload'],check=False)
 subprocess.run(['systemctl','restart','cnc-server.service'],check=False)
 subprocess.run(['systemctl','restart','cnc-ui.service'],check=False)
 raise
'''


def main():
    if not BINARY.is_file():
        raise RuntimeError('Build the Linux CNC server first: ' + str(BINARY))
    build_ui_archive()
    build_source_archive()
    with BINARY.open('rb') as source, gzip.open(BINARY_GZ, 'wb', compresslevel=9) as destination:
        shutil.copyfileobj(source, destination)
    server_sha = hashlib.sha256(BINARY.read_bytes()).hexdigest()
    ui_sha = hashlib.sha256(UI_TAR.read_bytes()).hexdigest()
    source_sha = hashlib.sha256(SOURCE_TAR.read_bytes()).hexdigest()
    release_name = 'drive-sync-' + server_sha[:16]
    release = '/opt/cnc/queue-releases/' + release_name
    remote('from pathlib import Path\np=Path(RELEASE);p.mkdir(parents=True,exist_ok=True);p.chmod(0o700)', RELEASE=release)
    upload(BINARY_GZ, '/tmp/cnc-server-drive-sync.gz')
    remote('''
import gzip,hashlib,os,shutil
from pathlib import Path
archive=Path('/tmp/cnc-server-drive-sync.gz'); target=Path('/tmp/cnc-server-drive-sync'); stage=target.with_name(target.name+'.unpack')
with gzip.open(archive,'rb') as source, stage.open('wb') as destination: shutil.copyfileobj(source,destination)
if hashlib.sha256(stage.read_bytes()).hexdigest()!=SERVER_SHA: raise RuntimeError('binary checksum mismatch after upload')
stage.chmod(0o755);stage.replace(target)
''', SERVER_SHA=server_sha)
    upload(UI_TAR, '/tmp/cnc-drive-sync-ui.tar.gz')
    upload(SOURCE_TAR, release + '/source.tar.gz')
    proof = json.loads(remote(ACTIVATE, RELEASE=release, SERVER_SHA=server_sha, UI_SHA=ui_sha))
    result = {'release': release_name, 'server_sha256': server_sha, 'ui_archive_sha256': ui_sha,
        'source_archive_sha256': source_sha, 'controller': proof, 'private_tools_accessed': False,
        'workers_replaced': False, 'job_started': False}
    evidence = Path('/tmp/cnc-drive-sync-rollout.json')
    evidence.write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()

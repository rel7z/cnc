#!/usr/bin/env python3
"""Deploy the localhost OAuth bridge by building the dashboard on the controller."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[2]
HOST = "cnc-linode-server"
SSH_CONFIG = ROOT / ".linode/ssh_config"
ARCHIVE = Path("/tmp/cnc-drive-bridge-ui-source.tar.gz")
FILES = [
    "components/dashboard/GDriveSettings.tsx",
    "app/drive-connect/route.ts",
    "app/drive-recover/page.tsx",
    "app/login/page.tsx",
    "lib/login-destination.ts",
    "app/drive-local-start/route.ts",
    "app/api/drive-connect-ticket/route.ts",
    "lib/drive-connect.ts",
    "lib/drive-connect-ticket.ts",
    "proxy.ts",
    "tests/gdrive-connect.test.mjs",
]


def remote(code, **values):
    prefix = "".join(key + "=" + repr(value) + "\n" for key, value in values.items())
    return subprocess.check_output(
        ["ssh", "-F", str(SSH_CONFIG), "-o", "BatchMode=yes", HOST, "python3", "-"],
        input=(prefix + code).encode(),
    ).decode()


def main():
    with tarfile.open(ARCHIVE, "w:gz") as archive:
        for name in FILES:
            source = ROOT / "cnc-ui" / name
            if not source.is_file() or source.is_symlink():
                raise RuntimeError("Missing dashboard source: " + str(source))
            archive.add(source, arcname=name, recursive=False)
    source_sha = hashlib.sha256(ARCHIVE.read_bytes()).hexdigest()
    release = "/opt/cnc/queue-releases/drive-oauth-bridge-" + source_sha[:16]
    subprocess.run([
        "scp", "-q", "-C", "-F", str(SSH_CONFIG),
        "-o", "ConnectTimeout=20", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=4",
        str(ARCHIVE), HOST + ":/tmp/cnc-drive-bridge-ui-source.tar.gz",
    ], check=True, timeout=120)
    proof = json.loads(remote(REMOTE, RELEASE=release, SOURCE_SHA=source_sha, FILES=FILES))
    result = {"release": Path(release).name, "source_sha256": source_sha, "controller": proof,
              "controller_binary_replaced": False, "workers_replaced": False, "private_tools_accessed": False}
    print(json.dumps(result, indent=2))


REMOTE = r'''
import hashlib,json,os,shutil,subprocess,tarfile,time,urllib.error,urllib.request
from pathlib import Path
release=Path(RELEASE);source=Path('/opt/cnc/source/cnc-ui');archive=Path('/tmp/cnc-drive-bridge-ui-source.tar.gz')
if hashlib.sha256(archive.read_bytes()).hexdigest()!=SOURCE_SHA:raise RuntimeError('source checksum mismatch')
if not (source/'package.json').is_file():raise RuntimeError('dashboard source is unavailable')
workers=json.load(urllib.request.urlopen('http://192.168.145.81:8080/api/workers',timeout=8))
if sum(1 for worker in workers if worker.get('status')=='online')!=6:raise RuntimeError('six workers are not online')
jobs=json.load(urllib.request.urlopen('http://192.168.145.81:8080/api/jobs',timeout=8))
if isinstance(jobs,dict):jobs=jobs.get('jobs',[])
if any(job.get('status') in ('queued','running','pending') for job in jobs):raise RuntimeError('controller has active jobs')
rollback=release/'rollback';rollback.mkdir(parents=True,exist_ok=True);rollback.chmod(0o700)
absent=[]
for name in FILES:
 current=source/name;backup=rollback/'source'/name
 if current.exists():
  if not current.is_file() or current.is_symlink():raise RuntimeError('unsafe source path '+name)
  backup.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(current,backup)
 else:absent.append(name)
(rollback/'absent.json').write_text(json.dumps(absent));(rollback/'absent.json').chmod(0o600)
with tarfile.open(archive) as bundle:
 members=bundle.getmembers()
 if sorted(member.name for member in members)!=sorted(FILES) or not all(member.isfile() and not member.issym() and not member.islnk() for member in members):raise RuntimeError('unexpected dashboard archive contents')
 bundle.extractall(source,filter='data')
npm='/opt/cnc/node-runtime/node-v22.23.3-linux-x64/bin/npm';node_dir=str(Path(npm).parent)
env=os.environ.copy();env['PATH']=node_dir+':'+env.get('PATH','');env['GO_API_URL']='http://192.168.145.81:8080'
dropin=Path('/etc/systemd/system/cnc-ui.service.d/zz-enum-version-pipeline.conf');original=dropin.read_bytes()
(rollback/'cnc-ui-dropin.conf').write_bytes(original);(rollback/'cnc-ui-dropin.conf').chmod(0o600)
try:
 subprocess.run([npm,'run','build','--','--webpack'],cwd=source,env=env,check=True,timeout=600,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
 target=release/'ui/.next/standalone'
 if target.parent.parent.exists():shutil.rmtree(target.parent.parent)
 target.parent.mkdir(parents=True,exist_ok=True)
 shutil.copytree(source/'.next/standalone',target)
 shutil.copytree(source/'.next/static',target/'.next/static')
 shutil.copytree(source/'public',target/'public')
 if not (target/'server.js').is_file():raise RuntimeError('standalone dashboard build is incomplete')
 pid=int(subprocess.check_output(['systemctl','show','cnc-ui.service','--property=MainPID','--value'],text=True).strip())
 node=Path(os.readlink(f'/proc/{pid}/exe'))
 lines=[line for line in original.decode().splitlines() if not line.startswith(('ExecStart=','WorkingDirectory=','Environment=GO_API_URL='))]
 lines+=['ExecStart=','ExecStart='+str(node)+' '+str(target/'server.js'),'WorkingDirectory='+str(target),'Environment=GO_API_URL=http://192.168.145.81:8080']
 stage=dropin.with_name(dropin.name+'.new');stage.write_text('\n'.join(lines)+'\n');stage.chmod(dropin.stat().st_mode&0o777);stage.replace(dropin)
 subprocess.run(['systemctl','daemon-reload'],check=True);subprocess.run(['systemctl','restart','cnc-ui.service'],check=True)
 def status(url):
  try:return urllib.request.urlopen(url,timeout=8).status
  except urllib.error.HTTPError as error:return error.code
  except urllib.error.URLError:return 0
 for attempt in range(60):
  if status('http://127.0.0.1:3000/login')==200:break
  time.sleep(1)
 else:raise RuntimeError('dashboard login is unhealthy')
 if status('http://127.0.0.1:3000/drive-connect')!=200:raise RuntimeError('OAuth recovery route is unavailable')
 if status('http://127.0.0.1:3000/drive-recover')!=200:raise RuntimeError('OAuth recovery page is unavailable')
 if status('http://127.0.0.1:3000/api/config/gdrive/callback')!=400:raise RuntimeError('OAuth callback is still blocked by dashboard login')
 auth=json.load(urllib.request.urlopen('http://192.168.145.81:8080/api/config/gdrive/auth-status',timeout=8))
 workers=json.load(urllib.request.urlopen('http://192.168.145.81:8080/api/workers',timeout=8))
 if sum(1 for worker in workers if worker.get('status')=='online')!=6:raise RuntimeError('worker health changed during dashboard deployment')
 print(json.dumps({'dashboard_active':True,'dashboard_login_http':200,'bridge_recovery_http':200,'callback_without_state_http':400,'workers_online':6,'drive_authorized':auth.get('authorized',False),'rollback_ui_dropin':str(rollback/'cnc-ui-dropin.conf'),'synced_source_files':FILES}))
except Exception:
 dropin.write_bytes(original);subprocess.run(['systemctl','daemon-reload'],check=False);subprocess.run(['systemctl','restart','cnc-ui.service'],check=False)
 for name in FILES:
  current=source/name;backup=rollback/'source'/name
  if backup.exists():current.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(backup,current)
  elif name in absent and current.exists():current.unlink()
 raise
'''


if __name__ == "__main__":
    main()

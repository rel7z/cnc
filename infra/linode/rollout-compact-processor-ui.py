#!/usr/bin/env python3
"""Deploy the compact dashboard and dedicated processor page."""
import concurrent.futures
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[2]
STAGE = Path('/tmp/cnc-compact-processor-ui-release')
HOSTS = ['cnc-linode-server'] + [f'cnc-linode-worker-{i}' for i in range(1, 7)]
SSH = ['ssh', '-F', str(ROOT / '.linode/ssh_config'), '-o', 'ConnectTimeout=10']


def remote(host, code, **values):
    prefix = ''.join(key + '=' + repr(value) + '\n' for key, value in values.items())
    return subprocess.check_output(SSH + [host, 'python3 -'], input=(prefix + code).encode()).decode()


def binary_snapshot():
    def inspect(host):
        code = '''import hashlib,json\nfrom pathlib import Path\npaths=['/opt/cnc/cnc-server','/opt/cnc/cnc-worker-linux','/opt/cnc-processor/cnc-processor'] if SERVER else ['/opt/cnc/cnc-worker']\nprint(json.dumps({path:hashlib.sha256(Path(path).read_bytes()).hexdigest() for path in paths}))'''
        return host, json.loads(remote(host, code, SERVER=host == HOSTS[0]))
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        return dict(pool.map(inspect, HOSTS))


ACTIVATE = r'''
import hashlib,json,subprocess,tarfile,time,urllib.request,urllib.error
from pathlib import Path
release=Path(RELEASE)
assert hashlib.sha256((release/'ui.tar.gz').read_bytes()).hexdigest()==DIGEST
with tarfile.open(release/'ui.tar.gz') as archive:
 assert all((m.isfile() or m.isdir()) and m.name.startswith('ui/') for m in archive.getmembers())
 archive.extractall(release,filter='data')
entry=release/'ui/.next/standalone/server.js'
assert entry.is_file()
config=Path('/etc/systemd/system/cnc-ui.service.d/zz-enum-version-pipeline.conf')
original=config.read_bytes()
backup=release/'rollback-ui.conf';backup.write_bytes(original);backup.chmod(0o600)
text=original.decode().splitlines()
text=[line for line in text if not line.startswith(('ExecStart=','WorkingDirectory='))]
text+=['ExecStart=','ExecStart=/opt/cnc/node-runtime/node-v22.23.3-linux-x64/bin/node '+str(entry),'WorkingDirectory='+str(entry.parent)]
try:
 temporary=config.with_name(config.name+'.new');temporary.write_text('\n'.join(text)+'\n');temporary.chmod(config.stat().st_mode & 0o777);temporary.replace(config)
 subprocess.run(['systemctl','daemon-reload'],check=True)
 subprocess.run(['systemctl','restart','cnc-ui'],check=True)
 for attempt in range(30):
  try:
   assert subprocess.check_output(['systemctl','is-active','cnc-ui'],text=True).strip()=='active'
   with urllib.request.urlopen('http://127.0.0.1:3000/login',timeout=5) as response:assert response.status==200
   request=urllib.request.Request('http://127.0.0.1:3000/api/tools/launch',data=b'{}',headers={'Content-Type':'application/json'})
   try:urllib.request.urlopen(request,timeout=5).close();raise RuntimeError('Unauthenticated launch accepted')
   except urllib.error.HTTPError as error:assert error.code==401
   break
  except Exception:
   if attempt==29:raise
   time.sleep(1)
except Exception:
 config.write_bytes(original)
 subprocess.run(['systemctl','daemon-reload'],check=True)
 subprocess.run(['systemctl','restart','cnc-ui'],check=True)
 raise
with urllib.request.urlopen('http://192.168.145.81:8080/api/workers',timeout=10) as response:workers=json.load(response)
assert len(workers)==6 and all(worker['status']=='online' for worker in workers)
print(json.dumps({'dashboard_login':200,'unauthenticated_launch':401,'workers_online':6,'only_ui_restarted':True,'rollback_config':str(backup)}))
'''


def main():
    STAGE.mkdir(exist_ok=True); STAGE.chmod(0o700)
    archive = STAGE / 'ui.tar.gz'
    with tarfile.open(archive, 'w:gz') as tar:
        for source, destination in [(ROOT/'cnc-ui/.next/standalone','ui/.next/standalone'), (ROOT/'cnc-ui/.next/static','ui/.next/standalone/.next/static'), (ROOT/'cnc-ui/public','ui/.next/standalone/public')]:
            assert source.is_dir()
            for path in sorted(source.rglob('*')):
                if path.is_symlink():raise RuntimeError('Unexpected dashboard build symlink')
                if path.is_file() and not path.name.startswith(".env"):tar.add(path, arcname=str(Path(destination)/path.relative_to(source)), recursive=False)
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    release = 'compact-processor-ui-' + digest[:16]
    directory = '/opt/cnc/queue-releases/' + release
    before = binary_snapshot()
    print('CNC binaries recorded on controller and six workers', flush=True)
    remote(HOSTS[0], 'from pathlib import Path\np=Path(RELEASE);p.mkdir(parents=True,exist_ok=True);p.chmod(0o700)', RELEASE=directory)
    subprocess.run(['scp','-q','-F',str(ROOT/'.linode/ssh_config'),str(archive),HOSTS[0]+':'+directory+'/ui.tar.gz'], check=True)
    print(remote(HOSTS[0], ACTIVATE, RELEASE=directory, DIGEST=digest), flush=True)
    after = binary_snapshot()
    assert before == after, 'CNC binary changed during UI deployment'
    result = {'release':release,'ui_sha256':digest,'deployed':True,'backend_binaries_unchanged':True,'binary_sha256':after,'only_ui_restarted':True,'kiro_accessed':False,'private_tools_accessed':False}
    (STAGE/'deployment.json').write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result), flush=True)


if __name__ == '__main__':main()

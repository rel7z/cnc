#!/usr/bin/env python3
"""Deploy unrestricted new enum launches, scanner, and dashboard together."""
import argparse
import concurrent.futures
import hashlib
import importlib.util
import json
import pathlib
import tarfile

ROOT = pathlib.Path(__file__).resolve().parents[2]
STAGE = pathlib.Path('/tmp/cnc-no-scope-release')
spec = importlib.util.spec_from_file_location('previous_rollout', ROOT/'infra/linode/rollout-smart-scanners.py')
previous = importlib.util.module_from_spec(spec)
spec.loader.exec_module(previous)
SERVER, WORKERS = previous.SERVER, previous.WORKERS
SOURCE_FILES = [
    'cnc-api/enum.go', 'cnc-api/enum_launcher_test.go',
    'cnc-backend/enum/pipeline.go',
    'cnc-ui/components/dashboard/ToolsLauncher.tsx',
    'cnc-ui/components/dashboard/VersionRuleEditor.tsx',
    'cnc-ui/lib/version-rules.ts',
    'cnc-ui/tests/optional-scope-ui.test.mjs',
    'cnc-ui/tests/scanner-options.test.mjs',
    'cnc-ui/tests/version-rules.test.mjs',
    'docs/CMS_VERSION_PIPELINE.md',
    'infra/linode/rollout-no-scope.py',
]

ACTIVATE_WORKER = r'''
manifest=json.loads((release/'manifest.json').read_text())
assert digest(release/'cnc-enum')==manifest['binaries']['cnc-enum']
destination='/opt/cnc/tools/cnc-enum'
subprocess.run(['systemctl','stop','cnc-worker'],check=True)
try:
 install(release/'cnc-enum',destination)
 subprocess.run(['systemctl','start','cnc-worker'],check=True)
 subprocess.run(['systemctl','is-active','--quiet','cnc-worker'],check=True)
except Exception:
 restore(destination)
 subprocess.run(['systemctl','start','cnc-worker'],check=False)
 raise
print(json.dumps({'host':HOST,'enum_installed':True}))
'''

ACTIVATE_SERVER = r'''
for _ in range(60):
 w=workers()
 jobs=json.load(urllib.request.urlopen('http://192.168.145.81:8080/api/jobs',timeout=10))
 if isinstance(jobs,dict):jobs=jobs.get('jobs',[])
 assert not any(j['status'] in ('running','queued','pending') for j in jobs),'New CNC job arrived; aborting activation'
 if len(w)==6 and all(x['status']=='online' and x['current_load']==0 for x in w):break
 time.sleep(1)
else:raise RuntimeError('Workers did not reconcile to idle')
manifest=json.loads((release/'manifest.json').read_text())
for name in ['ui.tar.gz','source.tar.gz']:
 assert digest(release/name)==manifest['archives'][name]
 unpack(name)
for name in ['cnc-server','cnc-enum']:
 assert digest(release/name)==manifest['binaries'][name]
config=pathlib.Path('/etc/systemd/system/cnc-ui.service.d/zz-enum-version-pipeline.conf')
original=config.read_bytes()
(release/'rollback/ui.conf').write_bytes(original)
paths={'cnc-server':'/opt/cnc/cnc-server','cnc-enum':'/opt/cnc/tools/cnc-enum'}
try:
 subprocess.run(['systemctl','stop','cnc-server'],check=True)
 for name,dst in paths.items():install(release/name,dst)
 entry=release/'ui/.next/standalone/server.js';assert entry.is_file()
 lines=[line for line in original.decode().splitlines() if not line.startswith(('ExecStart=','WorkingDirectory='))]
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
 else:raise RuntimeError('Workers did not reconnect to controller')
 assert urllib.request.urlopen('http://127.0.0.1:3000/login',timeout=8).status==200
 for name in SOURCE_FILES:
  src=release/'source'/name;dst=pathlib.Path('/opt/cnc/source')/name
  assert src.is_file() and not src.is_symlink()
  if dst.exists():
   assert dst.is_file() and not dst.is_symlink()
   saved=release/'rollback/source'/name;saved.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(dst,saved)
  dst.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(src,dst)
 print(json.dumps({'controller_active':True,'workers_online':6,'source_files_synced':len(SOURCE_FILES)}))
except Exception:
 subprocess.run(['systemctl','stop','cnc-ui','cnc-server'],check=False)
 for dst in paths.values():restore(dst)
 for name in SOURCE_FILES:
  saved=release/'rollback/source'/name
  if saved.exists():shutil.copy2(saved,pathlib.Path('/opt/cnc/source')/name)
 config.write_bytes(original);subprocess.run(['systemctl','daemon-reload'],check=False)
 subprocess.run(['systemctl','start','cnc-server','cnc-ui'],check=False)
 raise
'''

def package():
    STAGE.mkdir(parents=True, exist_ok=True)
    for name in ['cnc-server', 'cnc-enum']:
        assert (STAGE/name).is_file()
    with tarfile.open(STAGE/'ui.tar.gz', 'w:gz') as archive:
        for src, prefix in [(ROOT/'cnc-ui/.next/standalone', 'ui/.next/standalone'), (ROOT/'cnc-ui/.next/static', 'ui/.next/standalone/.next/static'), (ROOT/'cnc-ui/public', 'ui/.next/standalone/public')]:
            for file in sorted(src.rglob('*')):
                if file.is_symlink(): raise RuntimeError('Unexpected dashboard build symlink')
                if file.is_file(): archive.add(file, arcname=str(pathlib.Path(prefix)/file.relative_to(src)), recursive=False)
    with tarfile.open(STAGE/'source.tar.gz', 'w:gz') as archive:
        for name in SOURCE_FILES:
            file = ROOT/name
            assert file.is_file() and not file.is_symlink()
            archive.add(file, arcname='source/'+name, recursive=False)
    manifest = {'binaries': {name: previous.sha(STAGE/name) for name in ['cnc-server', 'cnc-enum']}, 'archives': {name: previous.sha(STAGE/name) for name in ['ui.tar.gz', 'source.tar.gz']}}
    manifest['release'] = '/opt/cnc/queue-releases/no-scope-' + hashlib.sha256(json.dumps(manifest, sort_keys=True).encode()).hexdigest()[:16]
    (STAGE/'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
    return manifest

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--stage-only', action='store_true')
    parser.add_argument('--activate', action='store_true')
    args = parser.parse_args()
    manifest = json.loads((STAGE/'manifest.json').read_text()) if args.activate else package()
    release = manifest['release']
    def stage(host):
        previous.remote(host, "from pathlib import Path\np=Path(RELEASE);p.mkdir(parents=True,exist_ok=True);p.chmod(0o700)", RELEASE=release)
        for name in ['cnc-enum', 'manifest.json']: previous.upload(host, STAGE/name, release+'/'+name)
        if host == SERVER:
            for name in ['cnc-server', 'ui.tar.gz', 'source.tar.gz']: previous.upload(host, STAGE/name, release+'/'+name)
    if not args.activate:
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool: list(pool.map(stage, [SERVER]+WORKERS))
        print(json.dumps({'staged':release}), flush=True)
    if args.stage_only: return
    values = {'RELEASE':release, 'SERVER':True}
    initial = json.loads(previous.remote(SERVER, previous.COMMON+previous.PREPARE, **values))
    updated = []
    try:
        for host in WORKERS:
            ready = json.loads(previous.remote(SERVER, previous.COMMON+"\nw=next(w for w in workers() if w['id']==HOST)\nprint(json.dumps(w['current_load']==0 and w['status']=='online'))", HOST=host, **values))
            assert ready, 'Worker became busy'
            print(previous.remote(host, previous.COMMON+ACTIVATE_WORKER, HOST=host, RELEASE=release, SERVER=False), flush=True)
            updated.append(host)
        print(previous.remote(SERVER, previous.COMMON+ACTIVATE_SERVER, SOURCE_FILES=SOURCE_FILES, **values), flush=True)
    except Exception:
        for host in updated:
            previous.remote(host, previous.COMMON+"\nrestore('/opt/cnc/tools/cnc-enum')\npid=subprocess.check_output(['systemctl','show','cnc-worker','--property=MainPID','--value'],text=True).strip()\nchildren=pathlib.Path('/proc/'+pid+'/task/'+pid+'/children').read_text().strip()\nif not children:subprocess.run(['systemctl','restart','cnc-worker'],check=True)", RELEASE=release, SERVER=False)
        raise
    finally:
        previous.remote(SERVER, previous.COMMON+"\napi('processing-state',{'enabled':ENABLED})", ENABLED=initial['previous_enabled'], **values)
    result = {'release':release, 'workers_online':6, 'rollback':release+'/rollback', 'sqlite_backup':release+'/rollback/processor.db'}
    (STAGE/'deployment.json').write_text(json.dumps(result, indent=2)+'\n')
    print(json.dumps(result), flush=True)

if __name__ == '__main__': main()

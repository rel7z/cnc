"""Deploy core processor controls on the migrated controller, without tool access."""
import hashlib
import json
from pathlib import Path
import shutil
import sqlite3
import subprocess
import sys
import time
import urllib.error
import urllib.request

stage = Path(sys.argv[1])
app = Path('/opt/cnc-controller')
processor = Path('/opt/cnc-processor')
artifacts = [(stage/'cnc-server-processor-controls-fix', app/'cnc-server', sys.argv[2]),
             (stage/'cnc-processor-controls-fix', processor/'cnc-processor', sys.argv[3])]
for source, target, digest in artifacts:
    if hashlib.sha256(source.read_bytes()).hexdigest() != digest:
        raise RuntimeError('Core artifact checksum mismatch')
env = dict(line.split('=', 1) for line in (app/'processor.env').read_text().splitlines() if '=' in line)
token = env['CNC_PROCESSOR_TOKEN']
address = json.loads((app/'config.json').read_text())['http_addr']
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def call(path, method='GET', data=None, base='http://127.0.0.1:9191'):
    request = urllib.request.Request(base+path, method=method,
        data=json.dumps(data).encode() if data is not None else None,
        headers={'Authorization': 'Bearer '+token, 'Content-Type': 'application/json'})
    with opener.open(request, timeout=10) as response:
        return json.load(response)


health = call('/v1/health')
if health['counts'].get('queued', 0) or health['counts'].get('running', 0):
    raise RuntimeError('Core rollout requires an idle processor')
jobs = call('/api/jobs', base='http://'+address)
if any(j['status'] in ('running', 'queued') for j in jobs):
    raise RuntimeError('Core rollout requires an idle controller')
# Explicitly approved before registration; never resume during this rollout.
call('/v1/processing-state', 'PUT', {'enabled': False})
backup = app/'processor-controls-rollback'/str(time.time_ns())
backup.mkdir(mode=0o700, parents=True)
for _, target, _ in artifacts:
    shutil.copy2(target, backup/target.name)
shutil.copy2(processor/'config.json', backup/'processor-config.json')
with sqlite3.connect(processor/'processor.db') as source, sqlite3.connect(backup/'processor.db') as target:
    source.backup(target)
replacement = app/('ui-controls-'+str(time.time_ns()))
shutil.copytree(stage/'ui/.next/standalone', replacement)
shutil.copytree(stage/'ui/.next/static', replacement/'.next/static', dirs_exist_ok=True)
if (stage/'ui/public').is_dir():
    shutil.copytree(stage/'ui/public', replacement/'public', dirs_exist_ok=True)
replacement.chmod(0o700)


def stop_ui():
    subprocess.run(['systemctl','stop','--no-block','cnc-ui'], check=True)
    deadline = time.monotonic()+12
    while time.monotonic()<deadline:
        state = subprocess.run(['systemctl','is-active','cnc-ui'],capture_output=True,text=True).stdout.strip()
        if state in ('inactive','failed'): return
        time.sleep(0.5)
    # SSE clients can keep this UI's already pending stop open. Only force
    # close the same service after confirming its authoritative stop state.
    state = subprocess.check_output(['systemctl','show','cnc-ui','--property=ActiveState','--value'],text=True).strip()
    job = subprocess.check_output(['systemctl','show','cnc-ui','--property=Job','--value'],text=True).strip()
    if state != 'deactivating' or not job or job == '0':
        raise RuntimeError('UI has no confirmed pending stop; refusing force-close')
    # Target the main UI process; some systemd releases report EINVAL for
    # auxiliary processes even after successfully terminating the main process.
    result = subprocess.run(['systemctl','kill','--kill-whom=main','--signal=SIGKILL','cnc-ui'],capture_output=True)
    if result.returncode:
        state = subprocess.check_output(['systemctl','show','cnc-ui','--property=ActiveState','--value'],text=True).strip()
        if state not in ('inactive','failed'):
            raise RuntimeError('Could not finish the confirmed UI stop')
    subprocess.run(['systemctl','stop','cnc-ui'],check=True,timeout=15)


ui_swapped = False
try:
    stop_ui()
    subprocess.run(['systemctl','stop','cnc-server'],check=True,timeout=45)
    subprocess.run(['systemctl','stop','cnc-processor'],check=True,timeout=45)
    (app/'ui').rename(backup/'ui')
    replacement.rename(app/'ui')
    ui_swapped = True
    for source, target, _ in artifacts:
        temp = target.with_name(target.name+'.controls-new')
        shutil.copy2(source,temp);temp.chmod(0o700);temp.replace(target)
    subprocess.run(['systemctl','start','cnc-server','cnc-processor','cnc-ui'],check=True,timeout=45)
    deadline = time.monotonic()+45
    while True:
        try:
            health = call('/v1/health')
            if health['enabled'] or health['counts'].get('running',0) or health['counts'].get('queued',0):
                raise RuntimeError('Processor did not retain its paused idle state')
            templates = call('/v1/processor-templates')
            if not {'builtin-processor-bf-20261021','builtin-processor-divi-20261021'}.issubset({x['id'] for x in templates}):
                raise RuntimeError('Default processor templates are missing')
            with opener.open('http://127.0.0.1:3000/login',timeout=5) as response:
                if response.status != 200: raise RuntimeError('UI login unavailable')
            try:
                call('/api/processor/checkpoints/reset','POST',{},base='http://127.0.0.1:3000')
                raise RuntimeError('Unauthenticated checkpoint reset was accepted')
            except urllib.error.HTTPError as error:
                if error.code != 401: raise
            break
        except (OSError, ValueError):
            if time.monotonic()>deadline: raise
            time.sleep(1)
except Exception:
    # Schema additions are backwards compatible. Keep live DB and its backup.
    for _, target, _ in artifacts:
        temp = target.with_name(target.name+'.controls-rollback')
        shutil.copy2(backup/target.name, temp)
        temp.replace(target)
    if ui_swapped:
        stop_ui()
        (app/'ui').rename(backup/'failed-ui')
        (backup/'ui').rename(app/'ui')
    subprocess.run(['systemctl','restart','cnc-server','cnc-processor','cnc-ui'],check=True,timeout=60)
    raise
maintenance = app/'maintenance'
maintenance.mkdir(mode=0o700,exist_ok=True)
shutil.copy2(stage/'processor_defaults.py',maintenance/'processor_defaults.py')
receipt = {'rollback':str(backup),'default_templates_seeded':True,'reset_requires_authentication':True,
           'processor_paused':True,'private_tools_accessed':False,
           'core_binary_sha256':{target.name:digest for _,target,digest in artifacts}}
(app/'processor-controls-receipt.json').write_text(json.dumps(receipt,indent=2)+'\n')
print(json.dumps(receipt))

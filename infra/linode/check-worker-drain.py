"""Read core worker/service metadata only, without inspecting task files."""
import hashlib
import json
from pathlib import Path
import subprocess

keys = ['MainPID', 'ControlPID', 'ActiveState', 'SubState', 'Result', 'Job', 'TimeoutStopUSec', 'KillMode']
raw = subprocess.check_output(['systemctl', 'show', 'cnc-worker'] + [item for key in keys for item in ['-p', key]], text=True)
properties = dict(line.split('=', 1) for line in raw.splitlines())
pid = int(properties.get('MainPID', '0'))
if pid:
    properties['running_core_sha256'] = hashlib.sha256(Path('/proc/' + str(pid) + '/exe').read_bytes()).hexdigest()
    children = set()
    for thread in Path('/proc/' + str(pid) + '/task').iterdir():
        try:
            children.update((thread / 'children').read_text().split())
        except FileNotFoundError:
            pass
    properties['child_count'] = len(children)
    capability = Path('/opt/cnc/data/worker-drain-ready.json')
    properties['drain_capability_matches_pid'] = capability.exists() and json.loads(capability.read_text()).get('pid') == pid
properties['installed_core_sha256'] = hashlib.sha256(Path('/opt/cnc/cnc-worker').read_bytes()).hexdigest()
properties['guard_sha256'] = hashlib.sha256(Path('/opt/cnc/worker-stop-guard').read_bytes()).hexdigest()
print(json.dumps(properties))

"""Activate verified core artifacts only; private programs are never executed.

Worker preparation mechanically copies already registered bundle bytes into the
owner-requested stable folders. It does not inspect program contents or sources.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import time
import urllib.request

stage = Path(sys.argv[1]).resolve()
role = sys.argv[2]
manifest = json.loads((stage / 'manifest.json').read_text())
backup = stage / 'rollback'
backup.mkdir(mode=0o700, exist_ok=True)


def checked(path, key):
    if hashlib.sha256(path.read_bytes()).hexdigest() != manifest[key]:
        raise RuntimeError('Core artifact checksum mismatch: ' + key)


def install(source, target):
    saved = backup / target.name
    if target.exists() and not saved.exists():
        shutil.copy2(target, saved)
    temp = target.with_name(target.name + '.workspace-new')
    shutil.copy2(source, temp)
    temp.chmod(0o700)
    os.replace(temp, target)


def run(*args):
    return subprocess.check_output(args, text=True).strip()


if role == 'worker':
    checked(stage / 'worker', 'worker')
    config = Path('/opt/cnc/config.json')
    original = json.loads(config.read_text())
    if not (backup / 'config.json').exists():
        shutil.copy2(config, backup / 'config.json')
    original['processor_workspace_root'] = '/root/processor'
    Path('/root/processor').mkdir(mode=0o700, exist_ok=True)
    temp = config.with_suffix('.workspace-new')
    temp.write_text(json.dumps(original, indent=2) + '\n'); temp.chmod(0o600)
    os.replace(temp, config)
    old_pid = run('systemctl', 'show', 'cnc-worker', '-p', 'MainPID', '--value')
    install(stage / 'worker', Path('/opt/cnc/cnc-worker'))
    # The stable guard survives both this upgrade and a later main-binary rollback.
    install(stage / 'worker', Path('/opt/cnc/worker-stop-guard'))
    run('systemctl', 'restart', '--no-block', 'cnc-worker')
    end = time.monotonic() + 45
    while time.monotonic() < end:
        raw = run('systemctl', 'show', 'cnc-worker', '-p', 'MainPID', '-p', 'ActiveState', '-p', 'SubState', '-p', 'Job')
        state = dict(line.split('=', 1) for line in raw.splitlines())
        if state['MainPID'] != old_pid and state['ActiveState'] == 'active' and state['SubState'] == 'running' and not state['Job']:
            checked(Path('/proc/' + state['MainPID'] + '/exe'), 'worker')
            break
        time.sleep(1)
    else:
        raise RuntimeError('Graceful restart still pending; do not force-stop active work')
    prepared = []
    for item in json.loads((stage / 'registered-versions.json').read_text()):
        version = item['version']
        root = Path(original['data_dir']) / 'processor-v2/bundles' / version
        if not root.is_dir():
            continue  # Normal controller installation prepares it when available.
        output = run('/opt/cnc/cnc-worker', '-config=/opt/cnc/config.json', '-prepare-workspaces', version)
        prepared.append({'program': item['program_id'], 'directory': output})
    print(json.dumps({'active_pid': state['MainPID'], 'workspaces': prepared, 'rollback': str(backup)}))
elif role == 'controller':
    for name in ('server', 'processor', 'worker', 'ui.tar.gz'):
        checked(stage / name, name)
    settings = {}
    for line in Path('/opt/cnc-controller/processor.env').read_text().splitlines():
        if '=' in line and not line.startswith('#'):
            key, value = line.split('=', 1); settings[key] = value.strip().strip('"').strip("'")
    req = urllib.request.Request('http://127.0.0.1:9191/v1/health', headers={'Authorization': 'Bearer ' + settings['CNC_PROCESSOR_TOKEN']})
    with urllib.request.urlopen(req, timeout=5) as response:
        health = json.load(response)
    if health['enabled'] or health['counts'].get('running', 0) or health['counts'].get('queued', 0):
        raise RuntimeError('Controller activation requires paused, idle processing')
    config = json.loads(Path('/opt/cnc-controller/config.json').read_text())
    with urllib.request.urlopen('http://' + config['http_addr'] + '/api/workers', timeout=5) as response:
        workers = json.load(response)
    if any(worker['current_load'] for worker in workers):
        raise RuntimeError('Wait for active worker jobs before controller restart')
    with urllib.request.urlopen('http://' + config['http_addr'] + '/api/jobs', timeout=5) as response:
        jobs = json.load(response)
    if any(job['status'] == 'running' for job in jobs):
        raise RuntimeError('Wait for active controller jobs before restart')
    ui = stage / 'ui'
    if ui.exists():
        raise RuntimeError('UI stage already exists; inspect before activation')
    ui.mkdir(mode=0o700)
    with tarfile.open(stage / 'ui.tar.gz') as archive:
        archive.extractall(ui, filter='data')
    assert (ui / 'server.js').is_file()
    services = ['cnc-ui', 'cnc-processor', 'cnc-server']
    targets = [('/opt/cnc-controller/cnc-server', 'server'), ('/opt/cnc-processor/cnc-processor', 'processor'), ('/opt/cnc-controller/cnc-worker-linux', 'worker')]
    old_ui = Path('/opt/cnc-controller/ui')
    changed_ui = False
    try:
        for service in services:
            run('systemctl', 'stop', service)
        for target, name in targets:
            install(stage / name, Path(target))
        os.replace(old_ui, backup / 'ui')
        changed_ui = True
        old_ui.symlink_to(ui, target_is_directory=True)
        for service in reversed(services):
            run('systemctl', 'start', service)
        deadline = time.monotonic() + 45
        while True:
            try:
                with urllib.request.urlopen('http://127.0.0.1:3000/login', timeout=3) as response:
                    assert response.status == 200
                break
            except Exception:
                if time.monotonic() >= deadline:
                    raise RuntimeError('Dashboard did not recover')
                time.sleep(1)
        print(json.dumps({'core_services': {service: run('systemctl', 'is-active', service) for service in services}, 'rollback': str(backup)}))
    except Exception:
        for service in services:
            subprocess.run(['systemctl', 'stop', service], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for target, _ in targets:
            saved = backup / Path(target).name
            if saved.is_file():
                shutil.copy2(saved, target)
        if changed_ui:
            old_ui.unlink(); os.replace(backup / 'ui', old_ui)
        for service in reversed(services):
            subprocess.run(['systemctl', 'start', service], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        raise
else:
    raise RuntimeError('Unknown activation role')

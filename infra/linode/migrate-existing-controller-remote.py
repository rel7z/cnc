"""Install core services on the owner-selected existing Linode, without tools."""
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import sqlite3
import subprocess
import sys
import time
import urllib.request


def ensure_merged_directory(app, home):
    """Keep the configured storage visible at ~/merged without duplicating data."""
    watch = app / 'merged'
    alias = home / 'merged'
    if watch.is_symlink():
        raise RuntimeError('Merged storage must be a real directory')
    watch.mkdir(mode=0o700, exist_ok=True)
    if alias.is_symlink() or alias.exists():
        if not alias.is_dir() or not alias.samefile(watch):
            raise RuntimeError('Existing ~/merged differs from configured storage; reconcile without overwriting')
    else:
        alias.symlink_to(watch, target_is_directory=True)
    return watch


os.umask(0o077)
stage = Path(sys.argv[1]).resolve()
app = Path('/opt/cnc-controller')
processor = Path('/opt/cnc-processor')
private_ip = '192.168.136.27'
manifest = json.loads((stage / 'artifact-manifest.json').read_text())
if 'processor_defaults.py' not in manifest or not (stage / 'processor_defaults.py').is_file():
    raise RuntimeError('Stage and checksum infra/processor_defaults.py with the core artifacts before installing')
for relative, expected in manifest.items():
    if hashlib.sha256((stage / relative).read_bytes()).hexdigest() != expected:
        raise RuntimeError('Staged core artifact checksum mismatch')
if app.exists() or processor.exists():
    raise RuntimeError('Destination already exists; reconcile instead of overwriting')
for service in ['cnc-server', 'cnc-ui', 'cnc-processor']:
    if (Path('/etc/systemd/system') / (service + '.service')).exists():
        raise RuntimeError('Existing core unit requires reconciliation: ' + service)
node_root = Path('/opt/cnc/node-path').read_text().strip()
node = Path(node_root) / 'bin/node'
if not node.is_file():
    raise RuntimeError('Existing Node runtime is unavailable')
credentials = json.loads((stage / 'restore/dashboard-access.json').read_text())
for key in ['username', 'password', 'session_secret']:
    if not credentials.get(key) or any(c in credentials[key] for c in '\r\n\x00'):
        raise RuntimeError('Saved dashboard credential format is invalid')
env = dict(os.environ, PATH=str(node.parent) + ':' + os.environ.get('PATH', ''),
           GO_API_URL='http://' + private_ip + ':8080', NEXT_TELEMETRY_DISABLED='1')
build_log = stage / 'ui-build.log'
with build_log.open('w') as output:
    for command in [['npm', 'ci', '--no-audit', '--no-fund'], ['npm', 'run', 'build', '--', '--webpack']]:
        result = subprocess.run(command, cwd=stage / 'ui', env=env, stdout=output, stderr=subprocess.STDOUT, timeout=600)
        if result.returncode:
            raise RuntimeError('Dashboard build failed; inspect the private staging log')
print('Dashboard Linux build passed', flush=True)
app.mkdir(mode=0o700)
processor.mkdir(mode=0o700)
for relative in ['data', 'ui']:
    (app / relative).mkdir(mode=0o700)
merged = ensure_merged_directory(app, Path.home())
(app / 'maintenance').mkdir(mode=0o700)
shutil.copy2(stage / 'processor_defaults.py', app / 'maintenance/processor_defaults.py')
shutil.copy2(stage / 'bin/cnc-server', app / 'cnc-server')
shutil.copy2(stage / 'bin/cnc-processor', processor / 'cnc-processor')
for path in [app / 'cnc-server', processor / 'cnc-processor']:
    path.chmod(0o700)
shutil.copytree(stage / 'ui/.next/standalone', app / 'ui', dirs_exist_ok=True)
shutil.copytree(stage / 'ui/.next/static', app / 'ui/.next/static', dirs_exist_ok=True)
if (stage / 'ui/public').is_dir():
    shutil.copytree(stage / 'ui/public', app / 'ui/public', dirs_exist_ok=True)
oauth = json.loads((stage / 'restore/google-oauth.json').read_text())
config = dict(http_addr=private_ip + ':8080', tcp_addr=private_ip + ':9090',
    data_dir=str(app / 'data'), local_admin_only=True, max_retries=0, heartbeat_ttl='30s',
    telegram={'enabled': False}, gdrive=dict(enabled=False, watch_dir=str(merged),
        client_id=oauth.get('client_id', ''), client_secret=oauth.get('client_secret', ''),
        redirect_url=oauth.get('redirect_url', ''), parent_folder_id=oauth.get('parent_folder_id', ''),
        token_path=str(app / 'gdrive-token.json'), state_file_path=str(app / 'gdrive-state.json'),
        upload_interval='1s', delete_after_upload=False))
(app / 'config.json').write_text(json.dumps(config, indent=2) + '\n')
(app / 'login.env').write_text('CNC_LOGIN_USERNAME=' + credentials['username'] + '\nCNC_LOGIN_PASSWORD=' + credentials['password'] + '\nCNC_SESSION_SECRET=' + credentials['session_secret'] + '\n')
token = secrets.token_urlsafe(48)
(app / 'processor.env').write_text('CNC_PROCESSOR_ENABLED=1\nCNC_PROCESSOR_TOKEN=' + token + '\nCNC_APP_DIR=' + str(processor) + '\nCNC_PROCESSOR_DB=' + str(processor / 'processor.db') + '\nCNC_WATCH_DIR=' + str(merged) + '\nCNC_PROCESSOR_BROKER_URL=http://' + private_ip + ':8080\nCNC_PROCESSOR_WORKER_URL=http://' + private_ip + ':8080/api/processor\n')
(app / 'dashboard-audit.env').write_text('CNC_DASHBOARD_AUDIT_SECRET=' + secrets.token_hex(32) + '\n')
(processor / 'config.json').write_text(json.dumps(dict(execution_mode='queue', routes={}, programs={},
    file_processing={'wordpress.txt': {'distribution': 'spread', 'require_summary': True}},
    auto_balance=dict(enabled=True, pause_cpu_percent=75, emergency_cpu_percent=90,
                      recover_cpu_percent=85, cooldown_seconds=10, interrupt_running=False))) + '\n')

def unit(name, working, command, extra=''):
    target = Path('/etc/systemd/system') / (name + '.service')
    if target.exists():
        raise RuntimeError('Existing service unit requires explicit reconciliation: ' + name)
    target.write_text('[Unit]\nDescription=CNC Linode migration ' + name + '\nAfter=network-online.target\nWants=network-online.target\n[Service]\nUser=root\nWorkingDirectory=' + str(working) + '\nUMask=0077\nEnvironmentFile=' + str(app / 'processor.env') + '\nEnvironmentFile=' + str(app / 'dashboard-audit.env') + '\n' + extra + 'ExecStart=' + command + '\nRestart=on-failure\nRestartSec=5\n[Install]\nWantedBy=multi-user.target\n')

unit('cnc-server', app, str(app / 'cnc-server') + ' -config=' + str(app / 'config.json'))
unit('cnc-processor', processor, str(processor / 'cnc-processor'))
unit('cnc-ui', app / 'ui', str(node) + ' ' + str(app / 'ui/server.js'),
     'TimeoutStopSec=15s\nKillMode=mixed\nEnvironment=HOSTNAME=127.0.0.1\nEnvironment=PORT=3000\nEnvironment=GO_API_URL=http://' + private_ip + ':8080\nEnvironmentFile=' + str(app / 'login.env') + '\n')
subprocess.run(['systemctl', 'daemon-reload'], check=True)
subprocess.run(['systemctl', 'enable', '--now', 'cnc-server', 'cnc-processor', 'cnc-ui'], check=True)
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
deadline = time.monotonic() + 45
while time.monotonic() < deadline:
    try:
        with opener.open('http://127.0.0.1:3000/login', timeout=3) as response:
            body = response.read(128 * 1024)
            if response.status != 200 or b'password' not in body:
                raise RuntimeError('Unexpected login page')
        request = urllib.request.Request('http://127.0.0.1:9191/v1/health', headers={'Authorization': 'Bearer ' + token})
        with opener.open(request, timeout=3) as response:
            health = json.load(response)
        if health['enabled']:
            raise RuntimeError('Fresh processor must remain paused')
        request = urllib.request.Request('http://127.0.0.1:9191/v1/processor-templates', headers={'Authorization': 'Bearer ' + token})
        with opener.open(request, timeout=3) as response:
            templates = json.load(response)
        if not {'builtin-processor-bf-20261021', 'builtin-processor-divi-20261021'}.issubset({item['id'] for item in templates}):
            raise RuntimeError('Processor artifact is missing the required default templates')
        break
    except (OSError, ValueError, RuntimeError):
        time.sleep(0.5)
else:
    raise RuntimeError('Core services did not become healthy; old worker configuration is unchanged')
receipt = dict(controller_private_ip=private_ip, dashboard_loopback_port=3000,
    core_services_healthy=True, processor_enabled=False, processor_routes_restored=False,
    aws_history_restored=False, drive_client_configured=bool(oauth.get('client_id')),
    drive_connected=False, private_tools_accessed=False, stage=str(stage))
receipt['processor_default_templates_seeded'] = True
(app / 'migration-receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
print(json.dumps(receipt), flush=True)

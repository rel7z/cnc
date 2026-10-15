#!/usr/bin/env python3
"""CNC dashboard tunnel and interactive first-time AWS/Linode deployment.

Python 3.9+, standard library only. Run with no arguments for the menu.
Deployment uses the CNC repository's source installers. The separate sync
command copies only the owner-authorized divi folder without running tools.
"""
import argparse
import getpass
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import plistlib
import secrets
import shlex
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import tarfile
import time
import urllib.error
import urllib.request
import webbrowser

LABEL = 'com.cnc.dashboard-manager'
LEGACY = 'com.cnc.aws-dashboard-tunnel'
SUPPORT = Path.home() / ('Library/Application Support/CNC Manager' if sys.platform == 'darwin' else '.config/cnc-manager')
DEFAULT_CONFIG = SUPPORT / 'config.json'
AWS_DOC = 'https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html'
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))

# Sync is intentionally separate from deployment and program activation.
SYNC_FOLDER = 'divi'
REMOTE_SYNC = r'''
import hashlib,json,os,shutil,stat,sys,tarfile,tempfile,time
from pathlib import Path,PurePosixPath
archive=Path(sys.argv[1]);expected=sys.argv[2]
if hashlib.sha256(archive.read_bytes()).hexdigest()!=expected:raise RuntimeError('Archive checksum mismatch')
destination=Path('/opt/cnc-processor/tools/divi')
for p in [destination,*destination.parents]:
 if p.is_symlink():raise RuntimeError('Symlink destination refused')
base=Path('/opt/cnc-processor/sync-rollback')
base.mkdir(mode=0o700,parents=True,exist_ok=True)
if base.is_symlink():raise RuntimeError('Symlink rollback directory refused')
run=Path(tempfile.mkdtemp(prefix='divi-',dir=base));stage=run/'incoming';stage.mkdir(mode=0o700)
with tarfile.open(archive,'r:gz') as package:
 members=package.getmembers()
 if len({x.name for x in members})!=len(members):raise RuntimeError('Duplicate archive paths')
 for member in members:
  path=PurePosixPath(member.name)
  if not member.isfile() or path.is_absolute() or '..' in path.parts or not path.parts:raise RuntimeError('Unsafe archive member')
  target=stage.joinpath(*path.parts);target.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
  with package.extractfile(member) as source,target.open('xb') as output:shutil.copyfileobj(source,output)
  target.chmod(member.mode & 0o777 & ~0o022)
manifest=json.loads((stage/'sync-manifest.json').read_text())
if set(manifest)!={str(p.relative_to(stage/'files')) for p in (stage/'files').rglob('*') if p.is_file()}:raise RuntimeError('Manifest mismatch')
for name,details in manifest.items():
 path=stage/'files'/name
 if hashlib.sha256(path.read_bytes()).hexdigest()!=details['sha256']:raise RuntimeError('Staged file checksum mismatch')
 target=destination/name
 for parent in [target,*target.parents]:
  if parent.is_symlink():raise RuntimeError('Symlink deployment target refused')
 if target.exists() and not target.is_file():raise RuntimeError('Deployment target is not a regular file')
destination.mkdir(mode=0o700,parents=True,exist_ok=True)
changed=[]
try:
 for name in manifest:
  target=destination/name;backup=run/'previous'/name
  target.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
  if target.exists():
   backup.parent.mkdir(mode=0o700,parents=True,exist_ok=True);shutil.copy2(target,backup)
  os.replace(stage/'files'/name,target);changed.append(name)
 for name,details in manifest.items():
  if hashlib.sha256((destination/name).read_bytes()).hexdigest()!=details['sha256']:raise RuntimeError('Installed file checksum mismatch')
except BaseException:
 for name in reversed(changed):
  target=destination/name;backup=run/'previous'/name
  if backup.exists():shutil.copy2(backup,target)
  # New files are retained: sync authorization forbids deleting tool files.
 raise
receipt={'folder':'divi','files':manifest,'rollback':str(run/'previous'),'verified':True,'activated':False}
(run/'receipt.json').write_text(json.dumps(receipt,indent=2));(run/'receipt.json').chmod(0o600)
print(json.dumps(receipt))
'''


class ManagerError(Exception):
    pass


def private_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.is_symlink():
        raise ManagerError('Refusing a symlink configuration file.')
    fd, name = tempfile.mkstemp(prefix='.cnc-', dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, 'w') as output:
            json.dump(value, output, indent=2)
            output.write('\n')
            output.flush()
            os.fsync(output.fileno())
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def read_json(path):
    try:
        return json.loads(Path(path).read_text())
    except (OSError, ValueError) as error:
        raise ManagerError('Cannot read configuration: ' + str(path)) from error


def ask(prompt, default=None):
    suffix = f' [{default}]' if default is not None else ''
    answer = input(prompt + suffix + ': ').strip()
    return answer or (str(default) if default is not None else '')


def port(value):
    number = int(value)
    if not 1024 <= number <= 65535:
        raise ManagerError('Local port must be between 1024 and 65535.')
    return number


def repo_path(explicit=None, config=None):
    candidates = [explicit, os.environ.get('CNC_REPO'), (config or {}).get('repo'),
                  Path(__file__).resolve().parent.parent, Path.cwd(), Path.home() / 'Documents/GitHub/cnc']
    for candidate in candidates:
        if candidate:
            path = Path(candidate).expanduser().resolve()
            if (path / 'cnc-api/go.mod').is_file() and (path / 'infra/aws/provision.py').is_file():
                return path
    raise ManagerError('CNC repository not found. Use --repo /path/to/cnc (or set CNC_REPO).')


def discover(repo):
    path = repo / '.aws-cnc/state.json'
    if path.exists():
        state = read_json(path)
        node = state.get('nodes', {}).get('cnc-aws-server', {})
        if node.get('id'):
            return dict(provider='aws', repo=str(repo), instance_id=node['id'],
                        region=state.get('region', 'us-east-2'), aws_profile='cnc', local_port=8081, remote_port=3000)
    path = repo / '.linode/state.json'
    if path.exists():
        state = read_json(path)
        if 'cnc-linode-server' in state.get('servers', {}):
            return dict(provider='linode', repo=str(repo), ssh_config=str(repo / '.linode/ssh_config'),
                        ssh_alias='cnc-linode-server', local_port=8081, remote_port=3000)
    return None


def load_config(path, repo=None):
    if Path(path).exists():
        cfg = read_json(path)
    else:
        cfg = discover(repo_path(repo))
        if not cfg:
            raise ManagerError('No deployment configured. Run setup for an existing server, or deploy for first-time creation.')
        private_json(path, cfg)
    if cfg.get('provider') not in ('aws', 'linode'):
        raise ManagerError('Configuration provider must be aws or linode.')
    cfg['local_port'] = port(cfg.get('local_port', 8081))
    remote = int(cfg.get('remote_port', 3000))
    if not 1 <= remote <= 65535:
        raise ManagerError('Remote port must be between 1 and 65535.')
    cfg['remote_port'] = remote
    return cfg


def executable(name, repo=None):
    paths = [shutil.which(name), Path.home() / '.local/bin' / name,
             Path('/opt/homebrew/bin') / name, Path('/usr/local/bin') / name]
    if repo and name == 'session-manager-plugin':
        paths += [Path(repo) / '.aws-cnc/bin' / name,
                  Path(repo) / '.aws-cnc/sessionmanagerplugin/bin' / name]
    paths += [Path.home() / 'Library/Application Support/CNC Dashboard Tunnel/bin' / name]
    for path in paths:
        if path and Path(path).is_file() and os.access(path, os.X_OK):
            return str(Path(path).resolve())
    raise ManagerError(f'{name} is missing. ' + (f'Install AWS CLI and Session Manager plugin: {AWS_DOC}' if name in ('aws', 'session-manager-plugin') else 'Install Git/OpenSSH first.'))


def tunnel_command(cfg):
    repo = cfg.get('repo')
    env = dict(os.environ)
    if cfg['provider'] == 'aws':
        plugin = executable('session-manager-plugin', repo)
        env['PATH'] = str(Path(plugin).parent) + os.pathsep + env.get('PATH', '')
        command = [executable('aws', repo), 'ssm', 'start-session', '--target', cfg['instance_id'],
                   '--document-name', 'AWS-StartPortForwardingSessionToRemoteHost', '--parameters',
                   json.dumps({'host': ['127.0.0.1'], 'portNumber': [str(cfg['remote_port'])],
                               'localPortNumber': [str(cfg['local_port'])]}),
                   '--region', cfg['region'], '--profile', cfg['aws_profile']]
    else:
        ssh_config = Path(cfg['ssh_config']).expanduser()
        if not ssh_config.is_file():
            raise ManagerError('SSH configuration is missing: ' + str(ssh_config))
        alias = cfg.get('ssh_alias', 'cnc-linode-server')
        if not alias or alias.startswith('-') or any(c.isspace() for c in alias):
            raise ManagerError('Invalid SSH host alias.')
        command = [executable('ssh'), '-F', str(ssh_config), '-o', 'BatchMode=yes', '-o', 'ExitOnForwardFailure=yes',
                   '-o', 'ServerAliveInterval=20', '-o', 'ServerAliveCountMax=3', '-N', '-L',
                   f'127.0.0.1:{cfg["local_port"]}:127.0.0.1:{cfg["remote_port"]}', alias]
    return command, env


def dashboard_health(local_port):
    try:
        with OPENER.open(f'http://127.0.0.1:{local_port}/login', timeout=4) as response:
            body = response.read(256 * 1024).lower()
            return response.status == 200 and b'password' in body and (b'username' in body or b'cnc' in body)
    except (OSError, urllib.error.URLError, TimeoutError):
        return False


def port_busy(local_port):
    try:
        with socket.create_connection(('127.0.0.1', local_port), timeout=1):
            return True
    except OSError:
        return False


def launchctl(*args, check=False):
    return subprocess.run(['launchctl', *args], capture_output=True, text=True, check=check)


def loaded(label):
    return sys.platform == 'darwin' and launchctl('print', f'gui/{os.getuid()}/{label}').returncode == 0


def manager_matches(path):
    agent = Path.home() / 'Library/LaunchAgents' / (LABEL + '.plist')
    if not agent.is_file() or not loaded(LABEL):
        return False
    data = plistlib.loads(agent.read_bytes())
    args = data.get('ProgramArguments', [])
    return '--config' in args and args[args.index('--config') + 1] == str(Path(path).resolve())


def legacy_matches(cfg):
    if cfg['provider'] != 'aws' or cfg['local_port'] != 8081:
        return False
    path = Path.home() / 'Library/Application Support/CNC Dashboard Tunnel/connect.sh'
    return path.is_file() and cfg['instance_id'] in path.read_text() and loaded(LEGACY)


def wait_ready(cfg, seconds=45):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if dashboard_health(cfg['local_port']):
            return True
        time.sleep(1)
    return False


def stop_child(child):
    if child.poll() is not None:
        return
    if os.name == 'posix':
        os.killpg(child.pid, signal.SIGTERM)
    else:
        child.terminate()
    try:
        child.wait(timeout=5)
    except subprocess.TimeoutExpired:
        if os.name == 'posix':
            os.killpg(child.pid, signal.SIGKILL)
        else:
            child.kill()
        child.wait()


def supervise(cfg, config_path, open_browser=False):
    command, env = tunnel_command(cfg)
    log = Path(config_path).parent / 'tunnel.log'
    log.parent.mkdir(parents=True, exist_ok=True)
    delay = 2
    opened = False
    running = True
    def stop(*_):
        nonlocal running
        running = False
    old_signals = {sig: signal.signal(sig, stop) for sig in (signal.SIGINT, signal.SIGTERM)}
    try:
        while running:
            if port_busy(cfg['local_port']):
                raise ManagerError('Tunnel port is already in use; stop the other tunnel or choose another local port.')
            with log.open('ab') as output:
                log.chmod(0o600)
                child = subprocess.Popen(command, env=env, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
                try:
                    misses = 0
                    while running and child.poll() is None:
                        if dashboard_health(cfg['local_port']):
                            delay = 2
                            misses = 0
                            if not opened:
                                print(f'Ready: http://localhost:{cfg["local_port"]}/dashboard', flush=True)
                                if open_browser:
                                    webbrowser.open(f'http://localhost:{cfg["local_port"]}/dashboard')
                                opened = True
                        else:
                            misses += 1
                            if misses >= 12:
                                print('Tunnel is stale; reconnecting. Check doctor if this repeats.', flush=True)
                                break
                        for _ in range(5):
                            if not running:
                                break
                            time.sleep(1)
                finally:
                    stop_child(child)
            if running:
                print(f'Retrying in {delay}s; log: {log}', flush=True)
                for _ in range(delay):
                    if not running:
                        break
                    time.sleep(1)
                delay = min(delay * 2, 30)
    finally:
        for sig, handler in old_signals.items():
            signal.signal(sig, handler)


def connect(cfg, path, no_browser=False, once=False):
    tunnel_command(cfg)  # Validate prerequisites before touching a managed process.
    if not dashboard_health(cfg['local_port']):
        label = LABEL if manager_matches(path) else LEGACY if legacy_matches(cfg) else None
        if label:
            launchctl('kickstart', '-k', f'gui/{os.getuid()}/{label}', check=True)
            if not wait_ready(cfg):
                raise ManagerError('Tunnel did not become healthy. Run doctor to check AWS/SSH and the remote dashboard.')
        elif once:
            raise ManagerError('No healthy tunnel. Run connect, or install for macOS auto-reconnect.')
        else:
            print('Keep this terminal open. Ctrl+C stops this foreground tunnel.')
            supervise(cfg, path, open_browser=not no_browser)
            return
    print(f'Ready: http://localhost:{cfg["local_port"]}/dashboard')
    if not no_browser:
        webbrowser.open(f'http://localhost:{cfg["local_port"]}/dashboard')


def install(cfg, path, no_browser=False):
    if sys.platform != 'darwin':
        raise ManagerError('Automatic login startup currently requires macOS. Use connect in a terminal on other systems.')
    tunnel_command(cfg)
    SUPPORT.mkdir(parents=True, exist_ok=True)
    SUPPORT.chmod(0o700)
    installed = SUPPORT / 'cnc_manager.py'
    if Path(__file__).resolve() != installed.resolve():
        shutil.copy2(__file__, installed)
    installed.chmod(0o700)
    agent = Path.home() / 'Library/LaunchAgents' / (LABEL + '.plist')
    agent.parent.mkdir(parents=True, exist_ok=True)
    previous = agent.read_bytes() if agent.exists() else None
    data = {'Label': LABEL, 'ProgramArguments': [sys.executable, str(installed), '--config', str(Path(path).resolve()), '_supervise'],
            'RunAtLoad': True, 'KeepAlive': True, 'ThrottleInterval': 10,
            'WorkingDirectory': str(SUPPORT),
            'StandardOutPath': str(SUPPORT / 'manager.log'), 'StandardErrorPath': str(SUPPORT / 'manager.log')}
    agent.write_bytes(plistlib.dumps(data));agent.chmod(0o600)
    old_legacy = legacy_matches(cfg)
    legacy_agent = Path.home() / 'Library/LaunchAgents' / (LEGACY + '.plist')
    legacy_backup = SUPPORT / (LEGACY + '.plist.rollback')
    if loaded(LABEL):
        launchctl('bootout', f'gui/{os.getuid()}/{LABEL}', check=True)
    if old_legacy:
        launchctl('bootout', f'gui/{os.getuid()}/{LEGACY}', check=True)
    try:
        if old_legacy:
            shutil.copy2(legacy_agent, legacy_backup)
            legacy_agent.unlink()
        launchctl('bootstrap', f'gui/{os.getuid()}', str(agent), check=True)
        if not wait_ready(cfg):
            raise ManagerError('New tunnel did not start; previous launcher restored. Run doctor.')
    except Exception:
        launchctl('bootout', f'gui/{os.getuid()}/{LABEL}')
        if previous is None:
            agent.unlink(missing_ok=True)
        else:
            agent.write_bytes(previous)
            launchctl('bootstrap', f'gui/{os.getuid()}', str(agent))
        if old_legacy:
            if legacy_backup.exists():
                shutil.copy2(legacy_backup, legacy_agent)
            launchctl('bootstrap', f'gui/{os.getuid()}', str(legacy_agent))
        raise
    print('Installed: auto-reconnect now and after login.')
    connect(cfg, path, no_browser=no_browser, once=True)


def doctor(cfg):
    command, env = tunnel_command(cfg)
    print('Local dashboard:', 'healthy' if dashboard_health(cfg['local_port']) else 'unavailable')
    print('Tunnel:', cfg['provider'], 'local port', cfg['local_port'], 'remote port', cfg['remote_port'])
    if cfg['provider'] == 'aws':
        result = subprocess.run([command[0], 'ssm', 'describe-instance-information', '--filters',
            'Key=InstanceIds,Values=' + cfg['instance_id'], '--profile', cfg['aws_profile'], '--region', cfg['region'], '--output', 'json'],
            env=env, capture_output=True, text=True, timeout=40)
        if result.returncode:
            raise ManagerError('AWS check failed. Verify the selected profile; for SSO run aws sso login --profile ' + cfg['aws_profile'])
        nodes = json.loads(result.stdout)['InstanceInformationList']
        print('AWS managed node:', nodes[0]['PingStatus'] if nodes else 'not registered with SSM')
    else:
        result = subprocess.run([executable('ssh'), '-F', cfg['ssh_config'], '-o', 'BatchMode=yes',
                                 '-o', 'ConnectTimeout=10', cfg['ssh_alias'], 'true'], capture_output=True, timeout=20)
        print('SSH:', 'reachable' if result.returncode == 0 else 'unavailable; check host, key and firewall')


def configure(path, repo=None):
    try:
        root = repo_path(repo)
    except ManagerError:
        root = repo_path(ask('Path to your CNC repository'))
    existing = discover(root) or {}
    provider = ask('Provider: aws or linode', existing.get('provider', 'aws')).lower()
    cfg = dict(provider=provider, repo=str(root), local_port=port(ask('Local dashboard port', 8081)),
               remote_port=int(ask('Remote dashboard port', 3000)))
    if provider == 'aws':
        cfg.update(instance_id=ask('EC2 controller instance ID', existing.get('instance_id')),
                   aws_profile=ask('AWS CLI profile', 'cnc'), region=ask('AWS region', existing.get('region', 'us-east-2')))
        if not cfg['instance_id'].startswith('i-'):
            raise ManagerError('An EC2 instance ID is required.')
    elif provider == 'linode':
        cfg.update(ssh_config=str(Path(ask('SSH config file', root / '.linode/ssh_config')).expanduser().resolve()),
                   ssh_alias=ask('SSH host alias', 'cnc-linode-server'))
    else:
        raise ManagerError('Choose aws or linode.')
    tunnel_command(cfg)
    private_json(path, cfg)
    return load_config(path)


def aws_runner(repo, profile, phase, *args):
    # A separate interpreter avoids changing this launcher's imports or globals.
    wrapper = """import importlib.util,json,sys
sys.path.insert(0,sys.argv.pop(1));import provision
provision.PROFILE=sys.argv.pop(1);phase=sys.argv.pop(1)
if phase=='plan':print(json.dumps(provision.inventory()))
elif phase=='provision':provision.main()
else:
 spec=importlib.util.spec_from_file_location('cnc_ssm_deploy',str(provision.ROOT/'infra/aws/ssm-deploy.py'))
 module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);module.main()
"""
    return [sys.executable, '-c', wrapper, str(repo / 'infra/aws'), profile, phase, *args]


def run_setup(command, repo, env=None):
    log = SUPPORT / 'setup.log'
    log.parent.mkdir(parents=True, exist_ok=True)
    with log.open('ab') as output:
        log.chmod(0o600)
        child = subprocess.Popen(command, cwd=repo, env=env, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
        print('Working; private setup log:', log, flush=True)
        try:
            while child.poll() is None:
                try:
                    child.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    print('.', end='', flush=True)
        except KeyboardInterrupt:
            stop_child(child)
            raise ManagerError('Setup interrupted. Saved provider state is retained for resumption.')
    print()
    if child.returncode:
        raise ManagerError('Setup stopped. Inspect the private log; rerun deploy to resume the recorded nodes.')


def linode_api(token, path):
    request = urllib.request.Request('https://api.linode.com/v4/' + path, headers={'Authorization': 'Bearer ' + token})
    try:
        with OPENER.open(request, timeout=35) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        raise ManagerError(f'Linode API returned HTTP {error.code}; check token permissions.') from None


def new_login(state_dir):
    file = state_dir / 'dashboard-access.json'
    if file.exists():
        return
    username = ask('New dashboard username', 'fahrel')
    password = getpass.getpass('New dashboard password: ')
    if not password or password != getpass.getpass('Repeat password: '):
        raise ManagerError('Passwords must be nonempty and match.')
    if any(c in username + password for c in '\r\n\x00'):
        raise ManagerError('Credentials cannot contain line breaks or NUL.')
    private_json(file, {'username': username, 'password': password, 'session_secret': secrets.token_urlsafe(48)})


def sync_archive(root, archive):
    """Opaque byte copy only: never import or parse the named tool files."""
    import io
    import stat
    source = root / 'processor/tools' / SYNC_FOLDER
    for path in (root / 'processor', source.parent, source):
        if path.is_symlink():
            raise ManagerError('Symlink source directory refused.')
    if not source.is_dir():
        raise ManagerError('Approved tool folder is missing: ' + str(source))
    manifest = {}
    class Copier:
        def __init__(self, file):
            self.file = file
            self.digest = hashlib.sha256()
        def read(self, size):
            data = self.file.read(size)
            self.digest.update(data)
            return data
    with tarfile.open(archive, 'w:gz') as package:
        for directory, dirs, files in os.walk(source, followlinks=False):
            for name in dirs + files:
                if (Path(directory) / name).is_symlink():
                    raise ManagerError('Symlink in approved folder; sync stopped.')
            for name in sorted(files):
                path = Path(directory) / name
                info = path.lstat()
                if not stat.S_ISREG(info.st_mode):
                    raise ManagerError('Non-regular source file refused.')
                relative = path.relative_to(source).as_posix()
                member = tarfile.TarInfo('files/' + relative)
                member.size = info.st_size
                member.mode = info.st_mode & 0o700
                fd = os.open(path, os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0))
                with os.fdopen(fd, 'rb') as stream:
                    if os.fstat(stream.fileno()).st_ino != info.st_ino:
                        raise ManagerError('Source changed while copying; retry sync.')
                    copier = Copier(stream)
                    package.addfile(member, copier)
                    after = os.fstat(stream.fileno())
                    if after.st_size != info.st_size or after.st_mtime_ns != info.st_mtime_ns:
                        raise ManagerError('Source changed while copying; retry sync.')
                manifest[relative] = {'bytes': info.st_size, 'sha256': copier.digest.hexdigest()}
        if not manifest:
            raise ManagerError('Approved tool folder has no regular files.')
        data = json.dumps(manifest, sort_keys=True).encode()
        member = tarfile.TarInfo('sync-manifest.json'); member.size = len(data); member.mode = 0o600
        package.addfile(member, io.BytesIO(data))
    Path(archive).chmod(0o600)
    return manifest


def sync_tools(cfg, path):
    """Sync the currently owner-authorized folder without enabling any route."""
    root = repo_path(config=cfg)
    receipt_file = Path(path).parent / 'last-sync.json'
    pending_file = Path(path).parent / 'pending-sync.json'
    def aws(*args):
        command = [executable('aws', root), *args, '--profile', cfg['aws_profile'], '--region', cfg['region'], '--output', 'json', '--no-cli-pager']
        result = subprocess.run(command, capture_output=True, text=True, timeout=90)
        if result.returncode:
            if args[:2] == ('ssm', 'get-command-invocation') and 'InvocationDoesNotExist' in result.stderr:
                return {'Status':'Pending'}
            if 'expired' in result.stderr.lower() or 'reauthenticate' in result.stderr.lower():
                raise ManagerError('AWS session expired. Run aws login --profile ' + cfg['aws_profile'] + ', then retry sync.')
            raise ManagerError('AWS sync request failed (' + ' '.join(args[:2]) + '); check credentials and permissions.')
        return json.loads(result.stdout) if result.stdout.strip() else {}
    def poll(command_id):
        print('Waiting for sync command:', command_id, flush=True)
        deadline = time.monotonic() + 600
        while time.monotonic() < deadline:
            try:
                result = aws('ssm', 'get-command-invocation', '--command-id', command_id, '--instance-id', cfg['instance_id'])
            except ManagerError:
                raise ManagerError('Unable to observe sync. Its command ID is saved; rerun sync to resume observation.') from None
            status = result['Status']
            if status == 'Success':
                return json.loads(result['StandardOutputContent'].strip().splitlines()[-1])
            if status in ('Failed', 'Cancelled', 'TimedOut'):
                pending_file.unlink(missing_ok=True)
                raise ManagerError('Remote sync ended with ' + status + '; previous files remain in rollback storage. Inspect SSM command ' + command_id)
            time.sleep(3)
        raise ManagerError('Sync is still pending. Its command ID is saved; rerun sync to resume observation.')
    if cfg['provider'] == 'aws' and pending_file.exists():
        pending = read_json(pending_file)
        if pending['instance_id'] != cfg['instance_id'] or pending['region'] != cfg['region']:
            raise ManagerError('Pending sync belongs to a different controller. Reconcile it before starting another sync.')
        receipt = poll(pending['command_id'])
    else:
        if cfg['provider'] == 'aws':
            # Authenticate before reading even opaque source bytes.
            identity = aws('sts', 'get-caller-identity')
            state = read_json(root / '.aws-cnc/state.json')
            if identity['Account'] != state.get('account'):
                raise ManagerError('AWS account does not match the recorded controller.')
            bucket = state.get('release_bucket')
            if not bucket:
                raise ManagerError('No existing private release bucket recorded; run the CNC source installer first.')
            block = aws('s3api', 'get-public-access-block', '--bucket', bucket)['PublicAccessBlockConfiguration']
            if not all(block.get(key) for key in ('BlockPublicAcls','IgnorePublicAcls','BlockPublicPolicy','RestrictPublicBuckets')):
                raise ManagerError('Release bucket must block all public access before private tool sync.')
        with tempfile.TemporaryDirectory(prefix='cnc-sync-') as temp:
            archive = Path(temp) / 'tools.tar.gz'
            manifest = sync_archive(root, archive)
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            if cfg['provider'] == 'aws':
                key = 'private-tool-sync/divi-' + digest + '.tar.gz'
                aws('s3api', 'put-object', '--bucket', bucket, '--key', key, '--body', str(archive), '--server-side-encryption', 'AES256')
                signed = subprocess.run([executable('aws',root), 's3', 'presign', f's3://{bucket}/{key}', '--expires-in', '900',
                    '--profile',cfg['aws_profile'],'--region',cfg['region']], capture_output=True,text=True,timeout=90)
                if signed.returncode:
                    raise ManagerError('Could not create the private transfer URL.')
                remote_archive = '/opt/cnc-processor/sync-incoming-' + digest + '.tar.gz'
                script = 'set -eu\numask 077\ncurl -fsSL --max-time 120 ' + shlex.quote(signed.stdout.strip()) + ' -o ' + shlex.quote(remote_archive)
                script += '\npython3 - ' + shlex.quote(remote_archive) + ' ' + digest + " <<'CNC_SYNC_PY'\n" + REMOTE_SYNC + '\nCNC_SYNC_PY\n'
                command = aws('ssm', 'send-command', '--instance-ids', cfg['instance_id'], '--document-name','AWS-RunShellScript',
                    '--comment','CNC owner-authorized divi file sync only', '--parameters',json.dumps({'commands':[script],'executionTimeout':['600']}))['Command']['CommandId']
                private_json(pending_file,dict(command_id=command,instance_id=cfg['instance_id'],region=cfg['region'],manifest=manifest))
                receipt = poll(command)
            else:
                # Validate alias/config using exactly the same rules as the tunnel.
                tunnel_command(cfg)
                remote_archive = '/tmp/cnc-divi-' + secrets.token_hex(12) + '.tar.gz'
                destination = cfg['ssh_alias'] + ':' + remote_archive
                scp = subprocess.run([executable('scp'),'-F',cfg['ssh_config'],'-o','BatchMode=yes',str(archive),destination],capture_output=True,timeout=120)
                if scp.returncode:raise ManagerError('SSH archive transfer failed.')
                remote = 'python3 - ' + shlex.quote(remote_archive) + ' ' + digest
                result = subprocess.run([executable('ssh'),'-F',cfg['ssh_config'],'-o','BatchMode=yes',cfg['ssh_alias'],remote],
                    input=REMOTE_SYNC,capture_output=True,text=True,timeout=180)
                if result.returncode:raise ManagerError('Remote sync failed; existing files have rollback copies.')
                receipt = json.loads(result.stdout.strip().splitlines()[-1])
            if receipt.get('files') != manifest:
                raise ManagerError('Remote receipt does not match the source manifest.')
    if not receipt.get('verified') or receipt.get('activated') is not False:
        raise ManagerError('Invalid remote sync receipt.')
    if pending_file.exists():
        if receipt.get('files') != read_json(pending_file)['manifest']:
            raise ManagerError('Remote receipt does not match the pending source manifest.')
    private_json(receipt_file, receipt)
    pending_file.unlink(missing_ok=True)
    print(f"Synced {len(receipt['files'])} files to /opt/cnc-processor/tools/divi; hashes verified.")
    print('Rollback:', receipt['rollback'])
    print('Routes unchanged; no private program was activated or run.')


def deploy(path, repo=None, no_browser=False):
    root = repo_path(repo)
    provider = ask('First-time deploy: aws or linode', 'aws').lower()
    if provider not in ('aws', 'linode'):
        raise ManagerError('Choose aws or linode.')
    state_dir = root / ('.aws-cnc' if provider == 'aws' else '.linode')
    state_file = state_dir / 'state.json'
    state = read_json(state_file) if state_file.exists() else {}
    if state.get('revision') or state.get('dashboard_verified') or state.get('verified_workers'):
        raise ManagerError('An existing verified deployment was found. Use setup/connect; first-time deploy will not replace it.')
    workers = int(ask('Number of workers', 3))
    if workers < 1 or workers > (7 if provider == 'aws' else 20):
        raise ManagerError('Unsupported worker count.')
    for tool in ('git', 'ssh', 'ssh-keygen'):
        executable(tool)
    if provider == 'aws':
        profile = ask('AWS CLI profile', 'cnc')
        aws_binary = executable('aws', root)
        executable('session-manager-plugin', root)
        os.environ['PATH'] = str(Path(aws_binary).parent) + os.pathsep + os.environ.get('PATH', '')
        print('AWS first-time installer region: us-east-2 (Ohio). Existing tunnels can use any region.')
        planned = subprocess.run(aws_runner(root, profile, 'plan'), capture_output=True, text=True)
        if planned.returncode:
            raise ManagerError('AWS planning failed. Configure credentials with aws configure --profile ' + profile + ', then retry; no servers created.')
        plan = json.loads(planned.stdout)
        if workers > plan['workers_launchable_now']:
            raise ManagerError('Selected workers exceed the current EC2 vCPU quota.')
        hourly = plan['controller_hourly_usd'] + workers * plan['worker_hourly_usd']
        print(f"Account {plan['account']}: 1 x {plan['instance_type']} + {workers} x {plan['worker_type']}: ${hourly:.5f}/hour compute. EBS, IPv4 and transfer are extra.")
        if ask('Type DEPLOY to create/resume these resources') != 'DEPLOY':
            print('Cancelled; no cloud resources changed.')
            return
        new_login(state_dir)
        run_setup(aws_runner(root, profile, 'provision', '--apply', '--workers', str(workers), '--accept-compute-hourly-usd', str(hourly)), root)
        run_setup(aws_runner(root, profile, 'deploy'), root)
        cfg = discover(root);cfg['aws_profile'] = profile
    else:
        token = os.environ.get('LINODE_TOKEN') or getpass.getpass('Linode API token (not saved): ')
        instances = linode_api(token, 'linode/instances?page_size=500')['data']
        recorded = {n['id'] for n in state.get('servers', {}).values()}
        if not recorded <= {n['id'] for n in instances}:
            raise ManagerError('Recorded Linodes are absent from this account. Reconcile saved state before resuming.')
        region = ask('Linode region', 'sg-sin-2')
        regions = linode_api(token, 'regions?page_size=500')['data']
        if region not in {r['id'] for r in regions}:
            raise ManagerError('Unknown Linode region.')
        types = {t['id']: t for t in linode_api(token, 'linode/types?page_size=500')['data']}
        server_type = ask('Controller plan', 'g6-standard-4')
        worker_type = ask('Worker plan', 'g6-standard-2')
        if server_type not in types or worker_type not in types:
            raise ManagerError('Unknown Linode plan.')
        monthly = types[server_type]['price']['monthly'] + workers * types[worker_type]['price']['monthly']
        print(f'1 x {server_type} + {workers} x {worker_type} in {region}: ${monthly:.2f}/month base. Backups/extra transfer are additional.')
        if ask('Type DEPLOY to create/resume these resources') != 'DEPLOY':
            print('Cancelled; no cloud resources changed.')
            return
        new_login(state_dir)
        env = dict(os.environ, LINODE_TOKEN=token)
        run_setup([sys.executable, str(root / 'infra/linode/provision.py'), '--apply', '--workers', str(workers),
                   '--region', region, '--server-type', server_type, '--worker-type', worker_type], root, env)
        ssh_config = state_dir / 'ssh_config'
        for name in read_json(state_file)['servers']:
            ready = False
            for attempt in range(60):
                result = subprocess.run([executable('ssh'), '-F', str(ssh_config), '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=10', name, 'true'], capture_output=True, timeout=20)
                if result.returncode == 0:
                    ready = True;break
                time.sleep(5)
            if not ready:
                raise ManagerError('SSH did not become ready for ' + name + '; rerun deploy to resume.')
        run_setup([sys.executable, str(root / 'infra/linode/release.py')], root, env)
        cfg = dict(provider='linode', repo=str(root), ssh_config=str(ssh_config), ssh_alias='cnc-linode-server', local_port=8081, remote_port=3000)
    if not cfg:
        raise ManagerError('Deployment state was not generated.')
    private_json(path, cfg)
    print('Deployment complete. Google Drive can be connected from the dashboard.')
    connect(cfg, path, no_browser=no_browser)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', type=Path, default=DEFAULT_CONFIG)
    parser.add_argument('--repo', type=Path)
    parser.add_argument('--no-browser', action='store_true')
    parser.add_argument('--once', action='store_true', help='Check/recover an existing managed tunnel, then return')
    parser.add_argument('action', nargs='?', choices=('connect', 'setup', 'deploy', 'doctor', 'install', 'stop', 'sync', '_supervise'))
    args = parser.parse_args()
    action = args.action
    if action is None:
        if not sys.stdin.isatty():
            parser.print_help();return
        print('CNC Manager\n1. Open dashboard\n2. Configure an existing server\n3. First-time AWS/Linode deployment\n4. Diagnose connection\n5. Install macOS auto-reconnect\n6. Stop managed tunnel\n7. Sync approved divi folder (files only)\nq. Quit')
        choice = ask('Choice', '1')
        action = {'1':'connect','2':'setup','3':'deploy','4':'doctor','5':'install','6':'stop','7':'sync','q':None}.get(choice)
        if action is None:
            return
    if action == 'deploy':
        deploy(args.config, args.repo, args.no_browser);return
    if action == 'setup':
        configure(args.config, args.repo);print('Saved. Run connect to open the dashboard.');return
    if action == 'stop':
        if sys.platform != 'darwin':
            raise ManagerError('Use Ctrl+C in the terminal running connect.')
        if loaded(LABEL):
            launchctl('bootout', f'gui/{os.getuid()}/{LABEL}', check=True)
        print('Managed tunnel stopped. Cloud services are unchanged.');return
    cfg = load_config(args.config, args.repo)
    if action == 'connect':connect(cfg, args.config, args.no_browser, args.once)
    elif action == 'doctor':doctor(cfg)
    elif action == 'install':install(cfg, args.config, args.no_browser)
    elif action == 'sync':sync_tools(cfg, args.config)
    elif action == '_supervise':supervise(cfg, args.config)


if __name__ == '__main__':
    try:
        main()
    except (ManagerError, ValueError, KeyError, OSError, subprocess.SubprocessError) as error:
        print('CNC Manager:', error, file=sys.stderr)
        sys.exit(1)
    except (KeyboardInterrupt, EOFError):
        print('\nCancelled.')
        sys.exit(130)

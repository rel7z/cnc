"""Stable, loopback-only Drive callback for the currently selected controller."""
import hashlib
import errno
from http.cookies import SimpleCookie
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import threading
import socket
import urllib.error
import urllib.parse
import urllib.request

CALLBACK_URL = 'http://localhost:8080/api/config/gdrive/callback'
PROTOCOL = 'cnc-drive-callback-v1'

# The key stays in the owner's private Manager directory, outside Drive/Git.
# A release-installed verifier authenticates cloud bytes before restoring them.
RECOVERY_SCRIPT = r'''
import base64,json,os,subprocess,sys,tempfile
from pathlib import Path
request=json.load(sys.stdin)
helper=Path('/opt/cnc/recovery/internet_map_recovery.py')
if not helper.is_file():
 print(json.dumps({'status':'installer-upgrade-required'}));sys.exit(0)
key=base64.b64decode(request['key'],validate=True)
if len(key)<32:raise RuntimeError('Invalid recovery key')
target=Path('/opt/cnc/data/internet-map-recovery.key')
if target.is_symlink():raise RuntimeError('Unsafe recovery key path')
target.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
if target.exists():
 if target.read_bytes()!=key:raise RuntimeError('Recovery key mismatch; saved key retained')
else:
 fd,name=tempfile.mkstemp(prefix='.recovery-key-',dir=target.parent)
 try:
  with os.fdopen(fd,'wb') as f:f.write(key)
  os.replace(name,target)
 finally:
  if os.path.exists(name):os.unlink(name)
target.chmod(0o600)
app=Path('/opt/internet-map')
if app.exists() and (not (app/'data/drive-recovery.json').is_file() or (app/'data/recovery-installed.json').is_file()):
 print(json.dumps({'status':'existing'}));sys.exit(0)
active=subprocess.run(['systemctl','is-active','--quiet','internet-map-recovery.service']).returncode==0
if not active:
 subprocess.run(['systemctl','start','--no-block','internet-map-recovery.service'],check=True,capture_output=True)
print(json.dumps({'status':'recovering'}))
'''


def sync_database_recovery(config_path, key_path):
    import base64
    cfg = json.loads(Path(config_path).read_text())
    if cfg.get('provider') not in ('linode', 'digitalocean') or not Path(key_path).is_file():
        return dict(status='not-configured')
    alias = cfg.get('ssh_alias', 'cnc-linode-server')
    if not alias or alias.startswith('-') or any(c.isspace() for c in alias):
        raise RuntimeError('Invalid controller SSH alias')
    auth, env = ssh_auth(cfg)
    command = ['ssh', '-F', str(Path(cfg['ssh_config']).expanduser()), *auth,
               '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=8', alias,
               'python3 -c ' + shlex.quote(RECOVERY_SCRIPT)]
    payload = dict(key=base64.b64encode(Path(key_path).read_bytes()).decode())
    result = subprocess.run(command, input=json.dumps(payload), text=True, capture_output=True, timeout=20, env=env)
    if result.returncode:
        raise RuntimeError('Database recovery setup failed over controller SSH; private key retained')
    return json.loads(result.stdout)

# Invoked only over the owner's existing, host-verified controller SSH connection.
# Tokens travel in captured stdin/stdout, never shell arguments or logs.
SESSION_SCRIPT = r'''
import hashlib,json,os,sys,tempfile
from pathlib import Path
request=json.load(sys.stdin)
config=next((p for p in (Path('/opt/cnc-controller/config.json'),Path('/opt/cnc/config.json')) if p.is_file()),None)
if config is None:raise RuntimeError('Controller configuration not found')
cfg=json.loads(config.read_text());drive=cfg.get('gdrive') or {}
client=request['client_id']
if drive.get('client_id') and drive['client_id']!=client:raise RuntimeError('Google client mismatch')
data=Path(cfg['data_dir']).resolve()
token_path=Path(drive.get('token_path') or data/'gdrive'/(hashlib.sha256(client.encode()).hexdigest()+'.json'))
if not token_path.is_absolute() or token_path.is_symlink():raise RuntimeError('Unsafe token path')
if request['action']=='restore':
 if drive.get('client_id'):raise RuntimeError('Session restore requires a fresh controller')
 token=request['token']
 if not isinstance(token,dict) or not token.get('refresh_token'):raise RuntimeError('No renewable Google session')
 token_path.parent.mkdir(parents=True,mode=0o700,exist_ok=True)
 if token_path.exists():
  if json.loads(token_path.read_text())!=token:raise RuntimeError('Refusing to overwrite a saved Google session')
 else:
  fd,name=tempfile.mkstemp(prefix='.session-',dir=token_path.parent)
  try:
   with os.fdopen(fd,'w') as f:json.dump(token,f)
   os.replace(name,token_path)
  finally:
   if os.path.exists(name):os.unlink(name)
 print(json.dumps({'restored':True}))
else:
 token=json.loads(token_path.read_text()) if token_path.is_file() else None
 print(json.dumps({'token':token}))
'''


def private_profile(path, profile):
    path = Path(path)
    if path.is_symlink():
        raise RuntimeError('Refusing a symlink Drive profile')
    fd, name = tempfile.mkstemp(prefix='.drive-profile-', dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as output:
            json.dump(profile, output)
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def ssh_auth(cfg):
    env = dict(os.environ)
    askpass = cfg.get('ssh_askpass')
    if not askpass:
        return ['-o', 'BatchMode=yes'], env
    helper = Path(askpass).expanduser()
    if not helper.is_absolute() or not os.access(helper, os.X_OK):
        raise RuntimeError('Configured SSH password helper is unavailable')
    env.update(SSH_ASKPASS=str(helper), SSH_ASKPASS_REQUIRE='force')
    return ['-o', 'BatchMode=no', '-o', 'StrictHostKeyChecking=yes'], env


def controller_session(config_path, client_id, token=None):
    cfg = json.loads(Path(config_path).read_text())
    if cfg.get('provider') not in ('linode', 'digitalocean'):
        return {}  # Existing AWS tunnels still support OAuth configuration.
    alias = cfg.get('ssh_alias', 'cnc-linode-server')
    if not alias or alias.startswith('-') or any(c.isspace() for c in alias):
        raise RuntimeError('Invalid controller SSH alias')
    auth, env = ssh_auth(cfg)
    command = ['ssh', '-F', str(Path(cfg['ssh_config']).expanduser()), *auth,
               '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=8', alias,
               'python3 -c ' + shlex.quote(SESSION_SCRIPT)]
    payload = dict(action='restore' if token else 'backup', client_id=client_id)
    if token:
        payload['token'] = token
    result = subprocess.run(command, input=json.dumps(payload), text=True, capture_output=True, timeout=20, env=env)
    if result.returncode:
        raise RuntimeError('Drive session transfer failed over controller SSH; saved backup retained')
    return json.loads(result.stdout)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args):
        return None


def opener():
    return urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())


def target(config_path):
    cfg = json.loads(Path(config_path).read_text())
    port = int(cfg['local_port'])
    if not 1024 <= port <= 65535 or port == 8080:
        raise RuntimeError('Dashboard must use a separate localhost port')
    return 'http://localhost:' + str(port), cfg


def handler(config_path):
    identity = hashlib.sha256(str(Path(config_path).resolve()).encode()).hexdigest()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass  # Authorization codes, state and tickets must never enter logs.

        def do_GET(self):
            if self.headers.get('Host') != 'localhost:8080':
                self.send_error(403, 'Unexpected callback host')
                return
            path = urllib.parse.urlsplit(self.path)
            try:
                base, _ = target(config_path)
            except (OSError, ValueError, KeyError, RuntimeError):
                self.send_error(503, 'CNC Manager has no dashboard connection')
                return
            if path.path == '/_cnc-drive/health':
                body = json.dumps(dict(protocol=PROTOCOL, callback_url=CALLBACK_URL, configuration_id=identity)).encode()
                self.send_response(200)
                origin = self.headers.get('Origin', '')
                parsed = urllib.parse.urlsplit(origin)
                try:
                    local_origin = parsed.scheme == 'http' and parsed.hostname in ('localhost', '127.0.0.1', '::1') and parsed.port == urllib.parse.urlsplit(base).port and not parsed.username and parsed.path in ('', '/')
                except ValueError:
                    local_origin = False
                if local_origin:
                    self.send_header('Access-Control-Allow-Origin', origin)
                    self.send_header('Vary', 'Origin')
                self.send_header('Cache-Control', 'no-store')
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(body)))
                self.end_headers(); self.wfile.write(body)
                return
            if path.path not in ('/drive-connect', '/api/config/gdrive/callback') or len(self.path) > 16384:
                self.send_error(404)
                return
            if path.path == '/drive-connect':
                query = urllib.parse.parse_qs(path.query)
                if set(query) != {'ticket'} or len(query['ticket']) != 1 or len(query['ticket'][0]) != 32:
                    self.send_error(400, 'Start a fresh connection from the dashboard')
                    return
            headers = {'X-Forwarded-Host': 'localhost:8080', 'X-Forwarded-Proto': 'http'}
            if path.path.endswith('/callback'):
                cookies = SimpleCookie(); cookies.load(self.headers.get('Cookie', ''))
                if 'cnc_drive_state' in cookies:
                    headers['Cookie'] = 'cnc_drive_state=' + cookies['cnc_drive_state'].value
            request = urllib.request.Request(base + self.path, headers=headers)
            try:
                try:
                    response = opener().open(request, timeout=30)
                except urllib.error.HTTPError as error:
                    response = error
                with response:
                    body = response.read((2 << 20) + 1)
                    if len(body) > 2 << 20:
                        raise RuntimeError('Callback response exceeded limit')
                    location = response.headers.get('Location')
                    if location:
                        parsed = urllib.parse.urlsplit(location)
                        if not parsed.netloc and location.startswith('/') and not location.startswith('//'):
                            location = base + location
                        elif not (parsed.scheme == 'https' and parsed.hostname == 'accounts.google.com' and not parsed.username):
                            raise RuntimeError('Unexpected callback redirect')
                        elif path.path == '/drive-connect':
                            profile_path = Path(config_path).parent / 'drive-oauth-profile.json'
                            profile = json.loads(profile_path.read_text()) if profile_path.is_file() else {}
                            account = profile.get('preferred_account', '')
                            if isinstance(account, str) and '@' in account and len(account) <= 254 and not any(c.isspace() for c in account):
                                query = [(k, v) for k, v in urllib.parse.parse_qsl(parsed.query) if k != 'login_hint']
                                query.append(('login_hint', account))
                                location = urllib.parse.urlunsplit(parsed._replace(query=urllib.parse.urlencode(query)))
                    self.send_response(response.status)
                    for key in ('Content-Type', 'Set-Cookie'):
                        for value in response.headers.get_all(key, []):
                            self.send_header(key, value)
                    if location:
                        self.send_header('Location', location)
                    self.send_header('Cache-Control', 'no-store')
                    self.send_header('Referrer-Policy', 'no-referrer')
                    self.send_header('Content-Length', str(len(body)))
                    self.end_headers(); self.wfile.write(body)
            except (OSError, ValueError, RuntimeError):
                self.send_error(503, 'Controller unavailable; reconnect CNC Manager and start again')

    return Handler


class CallbackServer(ThreadingHTTPServer):
    allow_reuse_address = True
    daemon_threads = True

    def handle_error(self, request, client_address):
        pass  # Never emit a traceback containing an OAuth request URL.


class IPv6CallbackServer(CallbackServer):
    address_family = socket.AF_INET6

    def server_bind(self):
        self.socket.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
        super().server_bind()


class CallbackGroup:
    def __init__(self, servers):
        self.servers = servers

    def shutdown(self):
        for server in self.servers:
            server.shutdown()

    def server_close(self):
        for server in self.servers:
            server.server_close()


def start_callback(config_path):
    servers = []
    try:
        servers.append(CallbackServer(('127.0.0.1', 8080), handler(config_path)))
        try:
            servers.append(IPv6CallbackServer(('::1', 8080), handler(config_path)))
        except OSError as error:
            if error.errno not in (errno.EAFNOSUPPORT, errno.EADDRNOTAVAIL, errno.EPROTONOSUPPORT):
                raise
    except OSError:
        for server in servers:
            server.server_close()
        raise RuntimeError('Drive callback port 8080 is occupied. Leave the Google callback unchanged and close the conflicting listener.') from None
    for server in servers:
        threading.Thread(target=server.serve_forever, daemon=True, name='cnc-drive-callback').start()
    return CallbackGroup(servers)


def controller_client(config_path):
    base, cfg = target(config_path)
    folder = {'linode': '.linode', 'digitalocean': '.digitalocean', 'aws': '.aws-cnc'}
    access_path = cfg.get('credentials_file') or str(Path(cfg['repo']) / folder[cfg['provider']] / 'dashboard-access.json')
    access = json.loads(Path(access_path).read_text())
    request = urllib.request.Request(base + '/api/auth/login',
        data=json.dumps({key: access[key] for key in ('username', 'password')}).encode(),
        headers={'Content-Type': 'application/json', 'Origin': base}, method='POST')
    with opener().open(request, timeout=10) as response:
        cookie = SimpleCookie(); cookie.load(response.headers.get('Set-Cookie', ''))
    if 'cnc_session' not in cookie:
        raise RuntimeError('Dashboard sign-in failed')

    def call(path, data=None):
        req = urllib.request.Request(base + path, data=json.dumps(data).encode() if data is not None else None,
            method='POST' if data is not None else 'GET', headers={'Cookie': 'cnc_session=' + cookie['cnc_session'].value, 'Content-Type': 'application/json', 'Origin': base})
        with opener().open(req, timeout=15) as response:
            return json.load(response)
    return call


def sync_controller(config_path, profile_path, enable=False):
    profile = json.loads(Path(profile_path).read_text())
    call = controller_client(config_path)
    saved = call('/api/config/gdrive')
    cfg = saved['gdrive']
    if cfg.get('client_id') and cfg['client_id'] != profile['client_id']:
        raise RuntimeError('Selected controller uses a different Google client; keep its profile separate')
    fresh = not cfg.get('client_id') and not saved.get('client_secret_set')
    if fresh and profile.get('oauth_token', {}).get('refresh_token'):
        controller_session(config_path, profile['client_id'], profile['oauth_token'])
    changed = cfg.get('redirect_url') != CALLBACK_URL
    cfg['redirect_url'] = CALLBACK_URL
    if not saved.get('client_secret_set'):
        cfg['client_id'], cfg['client_secret'] = profile['client_id'], profile['client_secret']
        if not cfg.get('parent_folder_id'):
            cfg['parent_folder_id'] = profile.get('parent_folder_id', '')
        changed = True
    if (enable or (fresh and profile.get('enabled', False))) and not cfg.get('enabled'):
        cfg['enabled'], changed = True, True
    if changed:
        call('/api/config/gdrive', cfg)
    # Capture renewable authorization after consent, so a fresh controller can
    # reconnect without asking the owner to paste secrets or consent again.
    status = call('/api/config/gdrive/auth-status')
    if status.get('authorized'):
        session = controller_session(config_path, profile['client_id'])
        token = session.get('token') or {}
        if token.get('refresh_token'):
            profile.update(oauth_token=token, enabled=cfg.get('enabled', False),
                           parent_folder_id=cfg.get('parent_folder_id', ''))
            private_profile(profile_path, profile)
    recovery = dict(status='not-configured')
    key_path = Path(profile_path).with_name('internet-map-recovery.key')
    if status.get('authorized') and cfg.get('enabled') and key_path.is_file():
        recovery = sync_database_recovery(config_path, key_path)
    return dict(callback_url=CALLBACK_URL, configured=True, updated=changed, database_recovery=recovery)


def begin_authorization(config_path, profile_path):
    with opener().open('http://localhost:8080/_cnc-drive/health', timeout=5) as response:
        health = json.load(response)
    identity = hashlib.sha256(str(Path(config_path).resolve()).encode()).hexdigest()
    if health.get('protocol') != PROTOCOL or health.get('configuration_id') != identity:
        raise RuntimeError('CNC Manager callback does not belong to this connection')
    sync_controller(config_path, profile_path, enable=True)
    result = controller_client(config_path)('/api/drive-connect-ticket', {})
    bridge = urllib.parse.urlsplit(result['url'])
    if bridge.scheme != 'http' or bridge.netloc != 'localhost:8080' or bridge.path != '/drive-connect':
        raise RuntimeError('Controller returned an unexpected Drive callback origin')
    return result['url']

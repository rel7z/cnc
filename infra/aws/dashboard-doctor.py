#!/usr/bin/env python3
"""Local-only dashboard tunnel repair and normal saved-credential sign-in.

Never deploys, restarts cloud services, runs tools, or changes processor state.
"""
import argparse
import configparser
import fcntl
import hmac
from http.cookies import SimpleCookie
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import os
from pathlib import Path
import plistlib
import secrets
import shlex
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

LABEL = 'com.cnc.aws-dashboard-tunnel'
URL = 'http://localhost:8081'
SUPPORT = Path.home() / 'Library/Application Support/CNC Dashboard Tunnel'


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def opener():
    return urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())


def atomic_json(path, data):
    tmp = path.with_suffix('.tmp')
    with open(tmp, 'w', opener=lambda p, flags: os.open(p, flags, 0o600)) as stream:
        json.dump(data, stream)
    tmp.chmod(0o600)
    tmp.replace(path)


def probe():
    try:
        with opener().open(URL + '/login', timeout=5) as response:
            body = response.read(128 * 1024)
            return response.status == 200 and b'name="username"' in body and b'name="password"' in body
    except (OSError, urllib.error.URLError):
        return False


def aws_command(root):
    binary = next((p for p in (Path.home()/'.local/bin/aws', Path('/opt/homebrew/bin/aws'), Path('/usr/local/bin/aws')) if p.is_file()), None)
    if not binary:
        raise RuntimeError('AWS CLI missing')
    state = json.loads((root / '.aws-cnc/state.json').read_text())
    env = dict(os.environ, AWS_PAGER='', AWS_CLI_AUTO_PROMPT='off')
    plugin_dirs = [SUPPORT/'bin', root/'.aws-cnc/bin', root/'.aws-cnc/sessionmanagerplugin/bin']
    env['PATH'] = os.pathsep.join(map(str, plugin_dirs)) + os.pathsep + os.environ.get('PATH', '/usr/bin:/bin')
    if not shutil.which('session-manager-plugin', path=env['PATH']):
        raise RuntimeError('AWS Session Manager plugin missing')
    return [str(binary)], state['nodes']['cnc-aws-server']['id'], state.get('region', 'us-east-2'), env


def classify_auth(code, error):
    if code == 0:
        return 'valid'
    text = error.lower()
    if any(s in text for s in ('signature expired', 'requestexpired', 'requesttimetooskewed', 'signature not yet current')):
        return 'clock_skew_or_stale_request'
    if any(s in text for s in ('expired', 'invalidclienttokenid', 'invalidgrant', 'unable to locate credentials', 'refresh token', 'sso session', 'aws login', 'login required', 'error loading sso token')):
        return 'login_required'
    return 'aws_unreachable_or_denied'


def auth_check(base, region, env):
    try:
        result = subprocess.run(base + ['sts', 'get-caller-identity', '--profile', 'cnc', '--region', region, '--cli-connect-timeout', '5', '--cli-read-timeout', '10'], env=env, capture_output=True, text=True, timeout=25)
        return classify_auth(result.returncode, result.stderr)
    except subprocess.TimeoutExpired:
        return 'aws_unreachable_or_denied'


def start_login(base, env, cache):
    now = time.time()
    previous = json.loads(cache.read_text()) if cache.exists() else {}
    if now - previous.get('attempted_at', 0) < 900:
        return False
    config = configparser.RawConfigParser()
    config.read(Path.home()/'.aws/config')
    sso = config.has_option('profile cnc', 'sso_session') or config.has_option('profile cnc', 'sso_start_url')
    login_env = dict(env)
    if sys.platform == 'darwin':
        chrome = Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
        if not chrome.is_file():
            raise RuntimeError('Google Chrome is required for AWS Incognito login')
        # AWS CLI uses Python's webbrowser module. Scope this preference to
        # the login child; dashboard links and the OS default stay unchanged.
        login_env['BROWSER'] = shlex.quote(str(chrome)) + ' --incognito %s'
    atomic_json(cache, {'attempted_at': now})
    subprocess.Popen(base + (['sso', 'login'] if sso else ['login']) + ['--profile', 'cnc'], env=login_env, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
    return True


def stop_tunnel_listener():
    """Only stop our user's Session Manager plugin occupying the CNC port."""
    result = subprocess.run(['/usr/sbin/lsof', '-nP', '-t', '-iTCP:8081', '-sTCP:LISTEN'], capture_output=True, text=True)
    for pid in set(result.stdout.split()):
        if not pid.isdigit():
            continue
        result = subprocess.run(['/bin/ps', '-p', pid, '-o', 'uid=,comm='], capture_output=True, text=True)
        parts = result.stdout.strip().split(None, 1)
        if len(parts) != 2 or parts[0] != str(os.getuid()) or Path(parts[1]).name != 'session-manager-plugin':
            raise RuntimeError('Port 8081 belongs to another program; left unchanged')
        os.kill(int(pid), signal.SIGTERM)
    for _ in range(20):
        check = subprocess.run(['/usr/sbin/lsof', '-nP', '-t', '-iTCP:8081', '-sTCP:LISTEN'], capture_output=True)
        if not check.stdout.strip():
            return
        time.sleep(0.1)
    raise RuntimeError('Previous tunnel has not released port 8081')


def manager_settings(root):
    path = Path.home() / 'Library/Application Support/CNC Manager/config.json'
    if not path.is_file():
        return None
    data = json.loads(path.read_text())
    if data.get('provider') != 'linode' or Path(data.get('repo', '/')).resolve() != root.resolve():
        return None
    port = int(data.get('local_port', 8081))
    if not 1024 <= port <= 65535:
        raise RuntimeError('Invalid manager dashboard port')
    return dict(data, _path=str(path))


def repair(root, allow_login):
    global URL
    cfg = manager_settings(root)
    if cfg:
        URL = 'http://localhost:' + str(cfg['local_port'])
    if probe():
        return {'status': 'healthy', 'dashboard': URL + '/dashboard'}
    if cfg:
        result = subprocess.run([sys.executable, str(root / 'infra/cnc_manager.py'),
            '--config', cfg['_path'], '--once', '--no-sync', '--no-browser', 'connect'],
            capture_output=True, text=True, timeout=55)
        refreshed = manager_settings(root) or cfg
        URL = 'http://localhost:' + str(refreshed['local_port'])
        if result.returncode == 0 and probe():
            return {'status': 'reconnected', 'dashboard': URL + '/dashboard', 'provider': 'linode'}
        return {'status': 'tunnel_failed', 'provider': 'linode', 'hint': 'Check the existing SSH manager and remote dashboard; no cloud service was restarted.'}
    base, instance, region, env = aws_command(root)
    auth = auth_check(base, region, env)
    if auth != 'valid':
        started = auth == 'login_required' and allow_login and start_login(base, env, root/'.aws-cnc/dashboard-login-state.json')
        return {'status': auth, 'login_browser_started': bool(started)}
    # A transient HTTP stall should not tear down an otherwise working tunnel.
    if probe():
        return {'status': 'healthy', 'dashboard': URL + '/dashboard'}
    stop_tunnel_listener()
    parameters = json.dumps({'host': ['127.0.0.1'], 'portNumber': ['3000'], 'localPortNumber': ['8081']})
    process = subprocess.Popen(base + ['ssm', 'start-session', '--target', instance, '--document-name', 'AWS-StartPortForwardingSessionToRemoteHost', '--parameters', parameters, '--region', region, '--profile', 'cnc'], env=env, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
    for _ in range(15):
        time.sleep(1)
        if probe():
            return {'status': 'reconnected', 'dashboard': URL + '/dashboard'}
        if process.poll() is not None:
            break
    # Do not leave a known-stalled forwarding process behind.
    if process.poll() is None:
        os.killpg(process.pid, signal.SIGTERM)
    return {'status': 'tunnel_failed', 'hint': 'AWS auth works; check controller SSM and loopback dashboard service. Cloud services were not restarted.'}


def session_cookie(root):
    global URL
    cfg = manager_settings(root)
    if cfg:
        URL = 'http://localhost:' + str(cfg['local_port'])
    credential_file = Path(cfg['credentials_file']) if cfg and cfg.get('credentials_file') else root/'.aws-cnc/dashboard-access.json'
    credentials = json.loads(credential_file.read_text())
    request = urllib.request.Request(URL + '/api/auth/login', data=json.dumps({k: credentials[k] for k in ('username', 'password')}).encode(), headers={'Content-Type': 'application/json', 'Origin': URL}, method='POST')
    with opener().open(request, timeout=10) as response:
        raw = response.headers.get('Set-Cookie', '')
    cookie = SimpleCookie(); cookie.load(raw)
    if 'cnc_session' not in cookie or cookie['cnc_session']['domain'] or cookie['cnc_session']['secure']:
        raise RuntimeError('Expected a localhost HTTP dashboard session cookie')
    # Verify this is a real usable session before offering it to the browser.
    request = urllib.request.Request(URL+'/dashboard', headers={'Cookie': 'cnc_session='+cookie['cnc_session'].value})
    with opener().open(request, timeout=10) as response:
        if response.status != 200:
            raise RuntimeError('Dashboard session verification failed')
    return cookie['cnc_session'].OutputString()


def realtime_check(root):
    try:
        cookie = session_cookie(root).split(';', 1)[0]
        request = urllib.request.Request(URL+'/api/events', headers={'Cookie': cookie, 'Accept': 'text/event-stream'})
        with opener().open(request, timeout=8) as response:
            if response.readline(256).strip() != b'event: snapshot':
                return {'status': 'invalid_snapshot'}
            line = response.readline(512 * 1024)
            if not line.endswith(b'\n'):
                return {'status': 'snapshot_too_large'}
            payload = json.loads(line.removeprefix(b'data: '))
            if not all(isinstance(payload.get(k), list) for k in ('workers', 'jobs', 'tasks')):
                return {'status': 'invalid_snapshot'}
            return {'status': 'connected', 'snapshot_bytes': len(line)}
    except urllib.error.HTTPError as error:
        return {'status': 'sign_in_rejected' if error.code in (401, 403) else 'backend_unavailable'}
    except (OSError, ValueError, urllib.error.URLError):
        return {'status': 'realtime_unavailable'}


def handoff(cookie, open_browser=False):
    """Single-use local link; session stays in HttpOnly cookie, never in URL."""
    token = secrets.token_urlsafe(32)
    consumed = False
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass
        def do_GET(self):
            nonlocal consumed
            valid = self.headers.get('Host') == 'localhost:'+str(self.server.server_port) and hmac.compare_digest(self.path, '/'+token) and not consumed
            if not valid:
                self.send_error(404)
                return
            consumed = True
            self.send_response(303)
            self.send_header('Set-Cookie', cookie)
            self.send_header('Location', URL+'/dashboard')
            self.send_header('Cache-Control', 'no-store')
            self.send_header('Referrer-Policy', 'no-referrer')
            self.send_header('Content-Security-Policy', "default-src 'none'; frame-ancestors 'none'")
            self.send_header('Content-Length', '0')
            self.end_headers()
    with HTTPServer(('127.0.0.1', 0), Handler) as server:
        link = 'http://localhost:'+str(server.server_port)+'/'+token
        print(link, flush=True)
        if open_browser:
            subprocess.Popen(['/usr/bin/open', link], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        deadline = time.monotonic()+60
        while not consumed and time.monotonic()<deadline:
            server.timeout = max(0.01, deadline-time.monotonic())
            server.handle_request()


def browser_link(root, open_browser):
    cookie = session_cookie(root)
    child = subprocess.Popen([sys.executable, str(Path(__file__).resolve()), '--handoff', '--dashboard-url', URL] + (['--open'] if open_browser else []), stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, start_new_session=True)
    child.stdin.write(cookie+'\n'); child.stdin.close()
    link = child.stdout.readline().strip(); child.stdout.close()
    if not link.startswith('http://localhost:'):
        raise RuntimeError('Local browser sign-in handoff failed')
    return link


def install(root):
    SUPPORT.mkdir(mode=0o700, parents=True, exist_ok=True); SUPPORT.chmod(0o700)
    destination = SUPPORT/'dashboard-doctor.py'
    shutil.copy2(Path(__file__).resolve(), destination); destination.chmod(0o700)
    config = {'Label': LABEL, 'ProgramArguments': [sys.executable, str(destination), '--root', str(root), '--repair', '--login'], 'RunAtLoad': True, 'StartInterval': 60, 'ProcessType': 'Background', 'StandardOutPath': str(SUPPORT/'doctor.log'), 'StandardErrorPath': str(SUPPORT/'doctor.log')}
    log = SUPPORT/'doctor.log'; log.touch(mode=0o600, exist_ok=True); log.chmod(0o600)
    agent = Path.home()/'Library/LaunchAgents'/ (LABEL+'.plist'); agent.parent.mkdir(exist_ok=True, parents=True)
    # Preserve the previous launcher for a local rollback.
    if agent.exists():
        shutil.copy2(agent, SUPPORT/'previous-agent.plist')
    agent.write_bytes(plistlib.dumps(config)); agent.chmod(0o600)
    domain = 'gui/'+str(os.getuid())
    subprocess.run(['/bin/launchctl', 'bootout', domain, str(agent)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.run(['/bin/launchctl', 'bootstrap', domain, str(agent)], check=True)
    app = Path.home()/'Applications/CNC Dashboard.app'
    executable = app/'Contents/MacOS/CNC Dashboard'
    executable.parent.mkdir(parents=True, exist_ok=True)
    executable.write_text('#!/bin/bash\nexec '+shlex.quote(sys.executable)+' '+shlex.quote(str(destination))+' --root '+shlex.quote(str(root))+' --repair --login --open\n')
    executable.chmod(0o700)
    (app/'Contents/Info.plist').write_bytes(plistlib.dumps({'CFBundleName': 'CNC Dashboard', 'CFBundleDisplayName': 'CNC Dashboard', 'CFBundleIdentifier': 'com.cnc.dashboard.launcher', 'CFBundleVersion': '1', 'CFBundlePackageType': 'APPL', 'CFBundleExecutable': 'CNC Dashboard', 'LSUIElement': True}))
    return {'status': 'installed', 'interval_seconds': 60, 'agent': str(agent), 'shortcut': str(app)}


def main():
    global URL
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument('--check', action='store_true')
    parser.add_argument('--repair', action='store_true')
    parser.add_argument('--login', action='store_true', help='Start official AWS browser login when expired (15 minute cooldown)')
    parser.add_argument('--install', action='store_true')
    parser.add_argument('--browser-link', action='store_true', help='Print a single-use, 60-second localhost sign-in link')
    parser.add_argument('--open', action='store_true', help='Open the signed-in dashboard in the default browser')
    parser.add_argument('--handoff', action='store_true', help=argparse.SUPPRESS)
    parser.add_argument('--dashboard-url', default=URL, help=argparse.SUPPRESS)
    args = parser.parse_args()
    if args.handoff:
        parsed = urllib.parse.urlsplit(args.dashboard_url)
        if parsed.scheme != 'http' or parsed.hostname != 'localhost' or not parsed.port or not 1024 <= parsed.port <= 65535 or parsed.path or parsed.query or parsed.fragment or parsed.username:
            raise RuntimeError('Handoff requires a localhost dashboard origin')
        URL = args.dashboard_url
        return handoff(sys.stdin.readline().strip(), args.open)
    root = args.root.resolve()
    cache = root/'.aws-cnc'; cache.mkdir(mode=0o700, exist_ok=True)
    try:
        if args.install:
            print(json.dumps(install(root))); return 0
        with (cache/'dashboard-doctor.lock').open('a') as lock:
            deadline = time.monotonic() + (45 if args.open or args.browser_link else 0)
            while True:
                try:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    break
                except BlockingIOError:
                    if time.monotonic() < deadline:
                        time.sleep(0.2)
                        continue
                    status = cache/'dashboard-doctor-status.json'
                    previous = json.loads(status.read_text()) if status.exists() else {'status': 'already_checking'}
                    print(json.dumps(dict(previous, checking_in_progress=True)))
                    return 0
            if args.repair or args.open or args.browser_link:
                result = repair(root, args.login)
            else:
                cfg = manager_settings(root)
                if cfg:
                    URL = 'http://localhost:' + str(cfg['local_port'])
                    result = {'status': 'healthy' if probe() else 'dashboard_unreachable', 'provider': 'linode'}
                else:
                    base, _, region, env = aws_command(root)
                    result = {'status': 'healthy' if probe() else 'dashboard_unreachable', 'aws_auth': auth_check(base, region, env)}
            if result['status'] in ('healthy', 'reconnected') and not (args.open or args.browser_link):
                result['realtime'] = realtime_check(root)
            atomic_json(cache/'dashboard-doctor-status.json', dict(result, checked_at=time.time()))
            if (args.open or args.browser_link) and result['status'] in ('healthy', 'reconnected'):
                link = browser_link(root, args.open)
                if args.browser_link:
                    print(link)
                else:
                    print(json.dumps({'status': 'signed_in_dashboard_opened'}))
            else:
                print(json.dumps(result))
            return 0 if result['status'] in ('healthy','reconnected') else 1
    except Exception as error:
        # Never echo auth bodies, cookies, CLI output, or credential values.
        print(json.dumps({'status': 'diagnosis_failed', 'error_type': type(error).__name__}), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())

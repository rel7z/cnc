#!/usr/bin/env python3
"""Publish the dashboard through an authenticated HTTP reverse proxy."""
import base64
import json
import os
from pathlib import Path
import secrets
import subprocess

ROOT = Path(__file__).resolve().parents[2]
STATE = ROOT / '.linode'


def main():
    state = json.loads((STATE / 'state.json').read_text())
    name = next(name for name in state['servers'] if name.endswith('-server'))
    address = state['servers'][name]['public_ip']
    access_path = STATE / 'dashboard-access.json'
    if access_path.exists() and state.get('public_auth') == 'form-session':
        access = json.loads(access_path.read_text())
    else:
        access = {'username': 'fahrel', 'password': 'fahrel'}
    access['url'] = 'http://' + address + '/dashboard'
    fd = os.open(access_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w') as output:
        json.dump(access, output, indent=2)
        output.write('\n')
    access_path.chmod(0o600)
    data = base64.b64encode(json.dumps({**access, 'address': address}).encode()).decode()
    script = '''set -eu
export DEBIAN_FRONTEND=noninteractive
if ! command -v nginx >/dev/null || ! command -v openssl >/dev/null; then
 apt-get update -qq
 apt-get install -y -qq nginx openssl
fi
python3 - <<'SETUP'
import base64,json,os,subprocess
from pathlib import Path
access=json.loads(base64.b64decode(os.environ['CNC_ACCESS']))
config=''' + repr('''server {
    listen 80;
    server_name PUBLIC_IP;
    client_max_body_size 100m;
    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto http;
        proxy_set_header Authorization "";
        proxy_buffering off;
        proxy_read_timeout 3600s;
    }
}
''') + '''
Path('/etc/nginx/sites-available/cnc').write_text(config.replace('PUBLIC_IP',access['address']))
link=Path('/etc/nginx/sites-enabled/cnc')
if not link.exists(): link.symlink_to('/etc/nginx/sites-available/cnc')
SETUP
nginx -t
systemctl enable nginx
systemctl restart nginx
'''
    subprocess.run(['ssh', '-F', str(STATE / 'ssh_config'), name, 'bash', '-s'], input='export CNC_ACCESS=' + data + '\n' + script, text=True, check=True)
    state['public_dashboard_url'] = access['url']
    state['public_auth'] = 'form-session'
    state['certificate'] = None
    (STATE / 'state.json').write_text(json.dumps(state, indent=2) + '\n')
    (STATE / 'state.json').chmod(0o600)
    print(json.dumps({'dashboard': access['url'], 'username': access['username'], 'credentials_file': str(access_path), 'protocol': 'http'}))

if __name__ == '__main__':
    main()

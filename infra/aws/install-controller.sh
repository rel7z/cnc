#!/usr/bin/env bash
# Runs as root on the EC2 controller after the source archive is extracted.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl git python3 python3-venv xz-utils unzip
install -d -m 700 /opt/cnc/data /opt/cnc-processor
export GOPATH=/opt/cnc/go-work
export GOMODCACHE=/opt/cnc/go-work/pkg/mod
export GOCACHE=/opt/cnc/go-cache
install -d -m 700 "$GOPATH" "$GOMODCACHE" "$GOCACHE"
if ! command -v aws >/dev/null 2>&1; then
  curl --fail --silent --show-error --location https://awscli.amazonaws.com/v2/install.sh -o /tmp/cnc-awscli-install.sh
  bash /tmp/cnc-awscli-install.sh --system
fi

python3 - <<'PY'
import hashlib, json, tarfile, urllib.request
from pathlib import Path
target = Path('/opt/cnc/go/bin/go')
if not target.exists():
    with urllib.request.urlopen('https://go.dev/dl/?mode=json', timeout=60) as response:
        releases = json.load(response)
    release = next(item for item in releases if item['stable'])
    artifact = next(item for item in release['files'] if item['os'] == 'linux'
                    and item['arch'] == 'amd64' and item['kind'] == 'archive')
    with urllib.request.urlopen('https://go.dev/dl/' + artifact['filename'], timeout=120) as response:
        data = response.read()
    if hashlib.sha256(data).hexdigest() != artifact['sha256']:
        raise RuntimeError('Go download checksum mismatch')
    archive = Path('/opt/cnc/go.tar.gz')
    archive.write_bytes(data)
    with tarfile.open(archive) as package:
        package.extractall('/opt/cnc', filter='data')
PY

cd /opt/cnc/source/processor
/opt/cnc/go/bin/go build -ldflags='-s -w' -o /opt/cnc-processor.new ./cmd/cnc-processor
cd /opt/cnc/source/cnc-api
/opt/cnc/go/bin/go build -ldflags='-s -w' -o /opt/cnc/cnc-server.new ./cmd/server
/opt/cnc/go/bin/go build -ldflags='-s -w' -o /opt/cnc/cnc-worker-linux ./cmd/worker

# This installer only builds unrestricted CNC backend tools and the Node runtime.
bash /opt/cnc/source/infra/upcloud/install-tools.sh

python3 - <<'PY'
import base64, json, os
from pathlib import Path
data = json.loads(base64.b64decode(os.environ['CNC_DEPLOY_DATA']))
path = Path('/opt/cnc/config.json')
current = json.loads(path.read_text()) if path.exists() else {}
config = {
    'http_addr': data['private_ip'] + ':8080',
    'tcp_addr': data['private_ip'] + ':9090',
    'data_dir': '/opt/cnc/data',
    'local_admin_only': True,
    'gdrive': current.get('gdrive', {'enabled': False}),
    'telegram': current.get('telegram', {'enabled': False}),
}
for key in ('wp_plugins_dir', 'joomla_plugins_dir'):
    if key in current:
        config[key] = current[key]
path.write_text(json.dumps(config, indent=2) + '\n')
Path('/opt/cnc/RELEASE').write_text(data['revision'] + '\n')
cloud = Path('/opt/cnc/aws-config.json')
cloud.write_text(json.dumps(data['aws_config'], indent=2) + '\n')
cloud.chmod(0o600)
access = Path('/opt/cnc/login.env')
if not access.exists():
    access.write_text('CNC_LOGIN_USERNAME=' + data['login_user'] + '\n'
                      'CNC_LOGIN_PASSWORD=' + data['login_password'] + '\n'
                      'CNC_SESSION_SECRET=' + data['session_secret'] + '\n')
access.chmod(0o600)
PY
python3 /opt/cnc/source/infra/aws/configure-dashboard-audit.py
if test -f /opt/cnc/cnc-server; then cp /opt/cnc/cnc-server /opt/cnc/cnc-server.previous; fi
mv /opt/cnc/cnc-server.new /opt/cnc/cnc-server
chmod 700 /opt/cnc/cnc-server
cat > /etc/systemd/system/cnc-server.service <<'UNIT'
[Unit]
Description=CNC controller
After=network-online.target
Wants=network-online.target
[Service]
User=root
WorkingDirectory=/opt/cnc
ExecStart=/opt/cnc/cnc-server -config=/opt/cnc/config.json
Restart=on-failure
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable cnc-server
systemctl restart cnc-server
systemctl is-active cnc-server

bash /opt/cnc/source/infra/aws/install-processor.sh

export PATH="$(cat /opt/cnc/node-path)/bin:$PATH"
cd /opt/cnc/source/cnc-ui
npm ci --no-audit --no-fund
PRIVATE_IP=$(python3 -c 'import base64,json,os; print(json.loads(base64.b64decode(os.environ["CNC_DEPLOY_DATA"]))["private_ip"])')
GO_API_URL="http://${PRIVATE_IP}:8080" npm run build -- --webpack
cp -r public .next/standalone/ 2>/dev/null || true
cp -r .next/static .next/standalone/.next/
NODE_BIN="$(cat /opt/cnc/node-path)/bin/node"
cat > /etc/systemd/system/cnc-ui.service <<UNIT
[Unit]
Description=CNC dashboard
After=network-online.target cnc-server.service
Wants=network-online.target
[Service]
User=root
WorkingDirectory=/opt/cnc/source/cnc-ui/.next/standalone
Environment=HOSTNAME=127.0.0.1
Environment=PORT=3000
Environment=GO_API_URL=http://${PRIVATE_IP}:8080
EnvironmentFile=/opt/cnc/login.env
EnvironmentFile=/opt/cnc/processor.env
ExecStart=${NODE_BIN} /opt/cnc/source/cnc-ui/.next/standalone/server.js
Restart=on-failure
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable cnc-ui
systemctl restart cnc-ui
systemctl is-active cnc-ui
curl --fail --silent --show-error "http://${PRIVATE_IP}:8080/api/workers" >/dev/null

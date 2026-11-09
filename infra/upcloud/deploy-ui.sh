#!/bin/sh
set -eu
cd "$(dirname "$0")/../.."
STATE_DIR="$PWD/.upcloud"
UI_ARCHIVE="$STATE_DIR/ui-source.tar.gz"
COPYFILE_DISABLE=1 tar --no-xattrs -czf "$UI_ARCHIVE" --exclude=node_modules --exclude=.next --exclude=.git -C cnc-ui .
scp -F "$STATE_DIR/ssh_config" "$UI_ARCHIVE" cnc-test-server:/opt/cnc/ui-source.tar.gz
PRIVATE_IP=$(python3 -c 'import json; print(json.load(open(".upcloud/state.json"))["servers"]["cnc-test-server"]["private_ip"])')
ssh -F "$STATE_DIR/ssh_config" cnc-test-server "PRIVATE_IP=$PRIVATE_IP bash -s" <<'REMOTE'
set -eu
python3 - <<'PY'
import urllib.request, json, hashlib, tarfile
from pathlib import Path
import sys
installed=Path('/opt/cnc/node-path')
if installed.exists() and (Path(installed.read_text().strip())/'bin/node').exists():
 sys.exit(0)
base='https://nodejs.org/dist/latest-v22.x/'
with urllib.request.urlopen(base+'SHASUMS256.txt',timeout=60) as r: sums=r.read().decode()
checksum, filename=next(line.split() for line in sums.splitlines() if line.endswith('-linux-x64.tar.xz'))
path=Path('/opt/cnc/node.tar.xz')
with urllib.request.urlopen(base+filename,timeout=120) as r: path.write_bytes(r.read())
if hashlib.sha256(path.read_bytes()).hexdigest()!=checksum: raise RuntimeError('Node checksum mismatch')
with tarfile.open(path) as archive: archive.extractall('/opt/cnc/node-runtime',filter='data')
node=next(Path('/opt/cnc/node-runtime').glob('node-*-linux-x64'))
Path('/opt/cnc/node-path').write_text(str(node))
PY
export PATH="$(cat /opt/cnc/node-path)/bin:$PATH"
mkdir -p /opt/cnc/ui
cd /opt/cnc/ui
tar -xzf /opt/cnc/ui-source.tar.gz
npm ci --no-audit --no-fund
GO_API_URL="http://$PRIVATE_IP:8080" npm run build -- --webpack
cp -r public .next/standalone/ 2>/dev/null || true
cp -r .next/static .next/standalone/.next/
NODE_BIN="$(cat /opt/cnc/node-path)/bin/node"
cat > /etc/systemd/system/cnc-ui.service <<UNIT
[Unit]
Description=CNC web dashboard
After=network-online.target cnc-server.service
[Service]
WorkingDirectory=/opt/cnc/ui/.next/standalone
Environment=HOSTNAME=127.0.0.1
Environment=PORT=3000
Environment=GO_API_URL=http://$PRIVATE_IP:8080
ExecStart=$NODE_BIN /opt/cnc/ui/.next/standalone/server.js
Restart=on-failure
RestartSec=5
TimeoutStopSec=10
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now cnc-ui
systemctl restart cnc-ui
sleep 3
curl -fsS -o /dev/null http://127.0.0.1:3000/dashboard
systemctl is-active cnc-ui
REMOTE

#!/usr/bin/env bash
# Runs as root on one EC2 worker; config arrives in CNC_DEPLOY_DATA.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq python3 ca-certificates curl
install -d -m 700 /opt/cnc/data
python3 - <<'PY'
import base64, json, os
from pathlib import Path
data = json.loads(base64.b64decode(os.environ['CNC_DEPLOY_DATA']))
Path('/opt/cnc/config.json').write_text(json.dumps({
    'server_addr': data['private_ip'] + ':9090',
    'worker_id': data['name'],
    'max_tasks': 2,
    'data_dir': '/opt/cnc/data',
    'processor_workspace_root': '/root/processor',
    'spread_workspace_root': '/root',
}) + '\n')
PY
SERVER_IP=$(python3 -c 'import base64,json,os; print(json.loads(base64.b64decode(os.environ["CNC_DEPLOY_DATA"]))["private_ip"])')
for attempt in $(seq 1 180); do
  if curl --fail --silent --show-error --connect-timeout 10 --max-time 120 "http://${SERVER_IP}:8080/download/cnc-worker-linux" -o /opt/cnc/cnc-worker.new && \
     curl --fail --silent --show-error --connect-timeout 10 --max-time 120 "http://${SERVER_IP}:8080/download/cnc-worker-tools" -o /opt/cnc/tools.tar.gz; then
    break
  fi
  if test "$attempt" -eq 180; then echo 'Controller downloads unavailable' >&2; exit 1; fi
  sleep 10
done
chmod 700 /opt/cnc/cnc-worker.new
mv /opt/cnc/cnc-worker.new /opt/cnc/cnc-worker
tar -xzf /opt/cnc/tools.tar.gz -C /opt/cnc
/opt/cnc/cnc-worker -drain-support >/dev/null
cp /opt/cnc/cnc-worker /opt/cnc/worker-stop-guard.new
chmod 700 /opt/cnc/worker-stop-guard.new
mv /opt/cnc/worker-stop-guard.new /opt/cnc/worker-stop-guard
NODE_BIN_DIR="$(cat /opt/cnc/node-path)/bin"
curl --fail --silent --show-error --connect-timeout 10 --max-time 30 "http://${SERVER_IP}:8080/download/cnc-release" -o /opt/cnc/RELEASE
cat > /etc/systemd/system/cnc-worker.service <<UNIT
[Unit]
Description=CNC worker
After=network-online.target
Wants=network-online.target
[Service]
User=root
WorkingDirectory=/opt/cnc
Environment=PATH=${NODE_BIN_DIR}:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ExecStart=/opt/cnc/cnc-worker -config=/opt/cnc/config.json
ExecStop=/opt/cnc/worker-stop-guard -config=/opt/cnc/config.json -drain-stop \$MAINPID
TimeoutStopSec=infinity
KillMode=mixed
Restart=on-failure
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable cnc-worker
systemctl restart cnc-worker
systemctl is-active cnc-worker

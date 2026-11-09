#!/usr/bin/env python3
"""Finish infrastructure bootstrap on the eight existing nodes; no tool runs."""
import concurrent.futures,json,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
source=(ROOT/'cnc-api/linode.go').read_text()
runtime=source.split("python3 - <<'NODE_RUNTIME'\n",1)[1].split('\nNODE_RUNTIME',1)[0]
script='''set -eu
for attempt in $(seq 1 48); do
 pid=$(systemctl show -p MainPID --value cloud-final)
 if test "$pid" = 0; then break; fi
 sleep 5
done
test "$(systemctl show -p MainPID --value cloud-final)" = 0
test -x /opt/cnc/cnc-worker
test -s /opt/cnc/config.json
systemctl is-active --quiet wg-quick@cncln0
python3 - <<'NODE_RUNTIME'
'''+runtime+'''
NODE_RUNTIME
NODE_BIN_DIR="$(cat /opt/cnc/node-path)/bin"
cat > /etc/systemd/system/cnc-worker.service <<UNIT
[Unit]
Description=CNC worker
After=network-online.target wg-quick@cncln0.service
Wants=network-online.target
Requires=wg-quick@cncln0.service
[Service]
User=root
WorkingDirectory=/opt/cnc
Environment=PATH=${NODE_BIN_DIR}:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ExecStart=/opt/cnc/cnc-worker -config=/opt/cnc/config.json
Restart=on-failure
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now cnc-worker >/dev/null
systemctl is-active cnc-worker
'''
def repair(i):
 name='cnc-linode-worker-'+str(i)
 r=subprocess.run(['ssh','-F',str(ROOT/'.linode/cross-cloud-ssh_config'),name,'bash','-s'],input=script,text=True,capture_output=True,timeout=500)
 return {'worker':name,'bootstrap_finished':r.returncode==0,'exit_code':r.returncode}
with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:results=list(pool.map(repair,range(1,9)))
print(json.dumps(results,indent=2));raise SystemExit(0 if all(x['bootstrap_finished'] for x in results) else 1)

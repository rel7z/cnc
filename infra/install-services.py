#!/usr/bin/env python3
"""Install private Internet Map systemd services on a CNC controller."""
import os
from pathlib import Path
import secrets
import subprocess
import sys

root = Path("/opt/internet-map")
(root / "datasets").mkdir(mode=0o700, parents=True, exist_ok=True)
node = next(Path("/opt/cnc/node-runtime").glob("*/bin/node"))
env = root / "service.env"
if not env.exists():
    fd = os.open(env, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as file:
        file.write("INTERNET_MAP_API_KEY=" + secrets.token_urlsafe(32) + "\n")
        file.write("INTERNET_MAP_API_URL=http://127.0.0.1:3500\n")
        file.write("HOSTNAME=127.0.0.1\nPORT=3501\n")
sys.path.insert(0, str(root))
from main import connect
db = connect(root / "data/catalog.db")
db.close()
units = Path("/etc/systemd/system")
common = "[Unit]\nAfter=network-online.target\nWants=network-online.target\nStartLimitIntervalSec=0\n[Service]\nWorkingDirectory=/opt/internet-map\nEnvironmentFile=/opt/internet-map/service.env\nRestart=always\nRestartSec=5\nUMask=0077\nNoNewPrivileges=true\nPrivateTmp=true\n"
# UI binds to loopback. Access is through the owner's private connection.
api = common + "ExecStart=/usr/bin/python3 /opt/internet-map/api.py --database /opt/internet-map/data/catalog.db\n[Install]\nWantedBy=multi-user.target\n"
ui = common + "Environment=PATH=" + str(node.parent) + ":/usr/bin:/bin\nWorkingDirectory=/opt/internet-map/web\nExecStart=" + str(node) + " /opt/internet-map/web/node_modules/next/dist/bin/next start -H 127.0.0.1 -p 3501\n[Install]\nWantedBy=multi-user.target\n"
(units / "internet-map-api.service").write_text(api.replace("\n", "\n"))
(units / "internet-map-ui.service").write_text(ui.replace("\n", "\n"))
subprocess.run(["systemctl", "daemon-reload"], check=True)
subprocess.run(["systemctl", "enable", "--now", "internet-map-api", "internet-map-ui"], check=True)

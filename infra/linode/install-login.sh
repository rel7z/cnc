#!/usr/bin/env bash
set -eu
mkdir -p /etc/systemd/system/cnc-ui.service.d
python3 - <<'PY'
from pathlib import Path
import secrets
import base64,json,os
p=Path('/opt/cnc/login.env')
if not p.exists():
    payload=json.loads(base64.b64decode(os.environ.get('DEPLOY_DATA','e30=')))
    login=payload.get('login') or {}
    username=login.get('username','fahrel');password=login.get('password','fahrel')
    if any(c in username+password for c in '\r\n\x00'):
        raise RuntimeError('Invalid dashboard credentials')
    def quote(value):return '"'+value.replace('\\','\\\\').replace('"','\\"')+'"'
    p.write_text('CNC_LOGIN_USERNAME='+quote(username)+'\nCNC_LOGIN_PASSWORD='+quote(password)+'\nCNC_SESSION_SECRET='+secrets.token_urlsafe(48)+'\n')
p.chmod(0o600)
PY
cat > /etc/systemd/system/cnc-ui.service.d/login.conf <<'UNIT'
[Service]
EnvironmentFile=/opt/cnc/login.env
UNIT
systemctl daemon-reload

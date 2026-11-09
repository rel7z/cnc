#!/usr/bin/env bash
# Called on the server from the exact Git release checkout.
set -eu
APP=/opt/cnc-processor
SOURCE=/opt/cnc/source/processor
mkdir -p "$APP/tools" /etc/systemd/system/cnc-server.service.d
if test ! -f "$APP/config.json"; then cp "$SOURCE/config.json" "$APP/config.json"; fi
# Install only the known fixture. Never replace any existing private tools.
if test ! -f "$APP/tools/test_program.py"; then cp "$SOURCE/tools/test_program.py" "$APP/tools/test_program.py"; fi
python3 - <<'PY'
import json,os,secrets
from pathlib import Path
p=Path('/opt/cnc/processor.env')
if not p.exists():
    config=json.loads(Path('/opt/cnc/config.json').read_text())
    watch=str(Path(config.get('gdrive',{}).get('watch_dir') or '~/merged').expanduser())
    if '\n' in watch or '"' in watch: raise RuntimeError('Invalid watch directory')
    p.write_text('CNC_PROCESSOR_ENABLED=1\nCNC_PROCESSOR_TOKEN='+secrets.token_urlsafe(48)+'\nCNC_APP_DIR=/opt/cnc-processor\nCNC_PROCESSOR_DB=/opt/cnc-processor/processor.db\nCNC_WATCH_DIR="'+watch+'"\n')
    p.chmod(0o600)
# Add broker addresses without replacing existing secrets or configuration.
config=json.loads(Path('/opt/cnc/config.json').read_text())
address=config['http_addr']
host=address.rsplit(':',1)[0]
if host in ('', '0.0.0.0', '::'):
    raise RuntimeError('Processor worker URL requires an explicit reachable HTTP address')
text=p.read_text()
for name,value in [('CNC_PROCESSOR_BROKER_URL','http://'+address),('CNC_PROCESSOR_WORKER_URL','http://'+address+'/api/processor')]:
    if not any(line.startswith(name+'=') for line in text.splitlines()):
        text += '\n'+name+'='+value+'\n'
p.write_text(text)
p.chmod(0o600)
PY
cat > /etc/systemd/system/cnc-server.service.d/processor.conf <<'UNIT'
[Service]
EnvironmentFile=/opt/cnc/processor.env
UNIT
systemctl stop cnc-processor 2>/dev/null || true
if test -f "$APP/processor.db"; then
 python3 - <<'PY'
import sqlite3,time
from pathlib import Path
p=Path('/opt/cnc-processor')
with sqlite3.connect(p/'processor.db') as source, sqlite3.connect(p/('processor-backup-'+str(time.time_ns())+'.db')) as dest:
    source.backup(dest)
PY
fi
if test -f "$APP/cnc-processor"; then cp "$APP/cnc-processor" "$APP/cnc-processor.previous"; fi
mv /opt/cnc-processor.new "$APP/cnc-processor"
chmod 700 "$APP/cnc-processor"
cp "$SOURCE/deploy/cnc-processor.service" /etc/systemd/system/cnc-processor.service
systemctl daemon-reload
systemctl enable cnc-processor
systemctl restart cnc-processor
systemctl is-active cnc-processor
cp /opt/cnc/source/processor/go.mod "$APP/deployed-go.mod"

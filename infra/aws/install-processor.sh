#!/usr/bin/env bash
# Runs as root on the controller. Never opens or copies the protected tools subtree.
set -euo pipefail
APP=/opt/cnc-processor
SOURCE=/opt/cnc/source/processor
install -d -m 700 "$APP" /etc/systemd/system/cnc-server.service.d
if test ! -f "$APP/config.json"; then
  printf '%s\n' '{"execution_mode":"queue","routes":{},"programs":{}}' > "$APP/config.json"
fi
python3 - <<'PY'
import json, secrets, sqlite3, time
from pathlib import Path
p = Path('/opt/cnc/processor.env')
if not p.exists():
    config = json.loads(Path('/opt/cnc/config.json').read_text())
    watch = str(Path(config.get('gdrive', {}).get('watch_dir') or '~/merged').expanduser())
    if '\n' in watch or '"' in watch:
        raise RuntimeError('Invalid watch directory')
    p.write_text('CNC_PROCESSOR_ENABLED=1\nCNC_PROCESSOR_TOKEN=' + secrets.token_urlsafe(48)
                 + '\nCNC_APP_DIR=/opt/cnc-processor\nCNC_PROCESSOR_DB=/opt/cnc-processor/processor.db'
                 + '\nCNC_WATCH_DIR="' + watch + '"\n')
address = json.loads(Path('/opt/cnc/config.json').read_text())['http_addr']
host = address.rsplit(':', 1)[0]
if host in ('', '0.0.0.0', '::'):
    raise RuntimeError('Processor worker URL needs a private controller address')
text = p.read_text()
for name, value in [('CNC_PROCESSOR_BROKER_URL', 'http://' + address),
                    ('CNC_PROCESSOR_WORKER_URL', 'http://' + address + '/api/processor')]:
    if not any(line.startswith(name + '=') for line in text.splitlines()):
        text += name + '=' + value + '\n'
p.write_text(text)
p.chmod(0o600)
database = Path('/opt/cnc-processor/processor.db')
if database.exists():
    with sqlite3.connect(database) as source, sqlite3.connect(
        database.with_name('processor-backup-' + str(time.time_ns()) + '.db')) as target:
        source.backup(target)
PY
cat > /etc/systemd/system/cnc-server.service.d/processor.conf <<'UNIT'
[Service]
EnvironmentFile=/opt/cnc/processor.env
UNIT
systemctl stop cnc-processor 2>/dev/null || true
if test -f "$APP/cnc-processor"; then cp "$APP/cnc-processor" "$APP/cnc-processor.previous"; fi
mv /opt/cnc-processor.new "$APP/cnc-processor"
chmod 700 "$APP/cnc-processor"
cp "$SOURCE/deploy/cnc-processor.service" /etc/systemd/system/cnc-processor.service
systemctl daemon-reload
systemctl enable cnc-processor
systemctl restart cnc-processor
systemctl is-active cnc-processor

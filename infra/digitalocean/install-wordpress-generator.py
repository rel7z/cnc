#!/usr/bin/env python3
"""Install the optional generator and keep a new instance paused."""
import json
from pathlib import Path
import shutil
import subprocess
import sys
import time

source = Path(sys.argv[1])
app = Path('/opt/cnc')
root = app / 'wordpress-generator'
root.mkdir(mode=0o700, exist_ok=True)
for name in ('reverse-ip', 'commoncrawl', 'candidate-txt'):
    (root / name).mkdir(mode=0o700, exist_ok=True)
if not (root / 'generator-state.db').exists():
    (root / 'paused').touch(mode=0o600, exist_ok=True)
for name in ('wordpress_generator.py', 'wordpress_ct.py', 'wordpress_bulk.py', 'wordpress_output.py', 'wordpress_restock.py', 'wordpress_reverse.py','wordpress_sources.py','wordpress_index.py','install-wordpress-index.py'):
    shutil.copy2(source / name, root / name)
# Reject mixed reader/source releases before activating the service.
subprocess.run([sys.executable, '-c',
                'import sys; sys.path.insert(0, sys.argv[1]); import wordpress_sources as s; '
                'assert callable(s.collection_records) and callable(s.recent_crawls)', str(root)], check=True)
if not (root / 'candidates.db').exists():
    subprocess.run([sys.executable, root / 'wordpress_bulk.py', root / 'candidates.db',
                    'import', 'archive', '/dev/null', '--format', 'txt'], check=True)
if not (root/'sources.json').exists():
    sys.path.insert(0,str(root))
    from wordpress_sources import update,COMMONCRAWL_ONLY
    update(root,COMMONCRAWL_ONLY)
subprocess.run([sys.executable,root/'install-wordpress-index.py','--root',root],check=True)
if not (root/'commoncrawl-mode.json').exists():
    from wordpress_sources import set_mode
    set_mode(root,'index')
config = json.loads((app / 'config.json').read_text())
env = root / 'generator.env'
if not env.exists():
    env.touch(mode=0o600)
    env.write_text('GO_API_URL=http://' + config['http_addr'] + '\n')
subprocess.run(['apt-get', 'install', '-y', 'python3-cryptography'], check=True)
for suffix in ('.service', '-refresh.service', '-refresh.timer'):
    name = 'cnc-wordpress-generator' + suffix
    shutil.copy2(source / name, Path('/etc/systemd/system') / name)
subprocess.run(['systemctl', 'daemon-reload'], check=True)
subprocess.run(['systemctl', 'enable', '--now', 'cnc-wordpress-generator.service',
                'cnc-wordpress-generator-refresh.timer'], check=True)
deadline = time.monotonic() + 30
while time.monotonic() < deadline:
    try:
        status = json.loads((root / 'status.json').read_text())
        if time.time() - status['updated_at'] < 20:
            break
    except FileNotFoundError:
        pass
    time.sleep(1)
else:
    raise RuntimeError('WordPress Generator did not publish status after installation')
for unit in ('cnc-wordpress-generator.service', 'cnc-wordpress-generator-refresh.timer'):
    subprocess.run(['systemctl', 'is-active', '--quiet', unit], check=True)

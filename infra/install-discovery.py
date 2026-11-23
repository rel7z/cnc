#!/usr/bin/env python3
"""Move local CNC discovery state to Internet Map without copying live SQLite."""
import json
import os
from pathlib import Path
import shutil
import shlex
import subprocess
import time
import urllib.request

ROOT = Path('/opt/internet-map')
OLD = Path('/opt/cnc/wordpress-generator')
STATE = ROOT / 'data/discovery'
UNITS = Path('/etc/systemd/system')
RESTORING = False
RETIRED_LEGACY = None


def run(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True)


def perform_install():
    global RESTORING, RETIRED_LEGACY
    config = json.loads(Path('/opt/cnc/config.json').read_text())
    op = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with op.open('http://' + config['http_addr'] + '/api/jobs?active=1', timeout=15) as response:
        if json.load(response):
            raise RuntimeError('Drain active CNC jobs before moving discovery state')
    if not OLD.is_symlink():
        RESTORING = (STATE.exists() and (ROOT / 'data/restore-receipt.json').is_file()
                     and (STATE / 'paused').is_file())
        if STATE.exists() and not RESTORING:
            raise RuntimeError('Destination state already exists; refusing overwrite')
        if OLD.exists() and not (OLD / 'paused').exists():
            raise RuntimeError('Pause discovery before migration')
        legacy_units = [name for name in ('cnc-wordpress-generator-refresh.timer',
                        'cnc-wordpress-generator-refresh.service', 'cnc-wordpress-generator.service')
                        if (UNITS / name).exists()]
        if legacy_units:
            run('systemctl', 'stop', *legacy_units)
        # Rename on the same volume preserves WAL files and every checkpoint.
        STATE.parent.mkdir(parents=True, exist_ok=True)
        if RESTORING:
            if OLD.exists():
                RETIRED_LEGACY = OLD.with_name('wordpress-generator-before-restore-' + str(time.time_ns()))
                OLD.rename(RETIRED_LEGACY)
        else:
            OLD.rename(STATE)
        OLD.symlink_to(STATE, target_is_directory=True)
    if OLD.resolve() != STATE.resolve():
        raise RuntimeError('Legacy discovery path belongs to another destination')
    if RESTORING or not (STATE / 'generator.env').exists():
        env = STATE / 'generator.env'
        env.write_text('GO_API_URL=http://' + config['http_addr'] + '\n')
        env.chmod(0o600)
    common = ('[Unit]\nAfter=network-online.target cnc-server.service\n'
              'Wants=network-online.target\n[Service]\nUser=root\nUMask=0077\n'
              'WorkingDirectory=/opt/internet-map\nNoNewPrivileges=true\n')
    generator = (common + 'EnvironmentFile=/opt/cnc/dashboard-audit.env\n'
                 'EnvironmentFile=/opt/internet-map/data/discovery/generator.env\n'
                 'ExecStart=/usr/bin/python3 /opt/internet-map/discovery/wordpress_generator.py '
                 '--shards --threads 256 --root /opt/internet-map/data/discovery '
                 '--catalog /opt/internet-map/data/discovery/candidates.db '
                 '--discovery-results /opt/internet-map/data/discovery/wordpress_discoveries_1m.jsonl\n'
                 'Restart=always\nRestartSec=5\n[Install]\nWantedBy=multi-user.target\n')
    (UNITS / 'internet-map-discovery.service').write_text(generator)
    for action, interval in [('sync', '30s'), ('backup', '6h')]:
        name = 'internet-map-' + action
        (UNITS / (name + '.service')).write_text(common + 'Type=oneshot\nExecStart=/usr/bin/python3 /opt/internet-map/maintenance.py ' + action + '\n')
        (UNITS / (name + '.timer')).write_text('[Unit]\nDescription=Internet Map ' + action + '\n[Timer]\nOnBootSec=2min\nOnUnitInactiveSec=' + interval + '\nPersistent=true\n[Install]\nWantedBy=timers.target\n')
    override = UNITS / 'internet-map-api.service.d'
    override.mkdir(exist_ok=True)
    (override / 'coordination.conf').write_text('[Service]\nEnvironmentFile=/opt/cnc/dashboard-audit.env\nEnvironment=INTERNET_MAP_CNC_URL=http://' + config['http_addr'] + '\n')
    values = dict(line.split('=', 1) for line in (ROOT/'service.env').read_text().splitlines() if '=' in line)
    key = shlex.split(values['INTERNET_MAP_API_KEY'])[0]
    if any(c in key for c in '\r\n'):
        raise RuntimeError('Invalid map API key')
    env = Path('/opt/cnc/internet-map.env')
    env.write_text('INTERNET_MAP_API_URL=http://127.0.0.1:3500\nINTERNET_MAP_API_KEY=' + key + '\n')
    env.chmod(0o600)
    ui_override = UNITS/'cnc-ui.service.d'
    ui_override.mkdir(exist_ok=True)
    (ui_override/'internet-map.conf').write_text('[Service]\nEnvironmentFile=/opt/cnc/internet-map.env\n')
    legacy_units = [name for name in ('cnc-wordpress-generator.service', 'cnc-wordpress-generator-refresh.timer')
                    if (UNITS / name).exists()]
    if legacy_units:
        run('systemctl', 'disable', *legacy_units)
    run('systemctl', 'daemon-reload')
    run('systemctl', 'disable', '--now', 'internet-map-backup.timer')
    run('systemctl', 'enable', '--now', 'internet-map-discovery.service', 'internet-map-sync.timer')
    run('systemctl', 'restart', 'internet-map-api.service')
    (STATE / 'cutover.json').write_text(json.dumps({'owner': 'internet-map', 'executor': 'cnc', 'migrated_at': int(time.time()), 'legacy_alias': str(OLD)}) + '\n')
    run('systemctl', 'is-active', '--quiet', 'internet-map-discovery.service', 'internet-map-api.service')
    print(json.dumps({'owner': 'internet-map', 'state': str(STATE), 'scanning_paused': (STATE / 'paused').exists()}))


def install():
    original_enabled = {}
    for unit in ('cnc-wordpress-generator.service', 'cnc-wordpress-generator-refresh.timer'):
        original_enabled[unit] = subprocess.run(['systemctl', 'is-enabled', unit], capture_output=True, text=True).returncode == 0
    migrated_before = OLD.is_symlink()
    try:
        perform_install()
    except BaseException:
        for unit in ('internet-map-discovery.service', 'internet-map-sync.timer', 'internet-map-backup.timer'):
            subprocess.run(['systemctl', 'disable', '--now', unit], capture_output=True)
        if not migrated_before and OLD.is_symlink() and OLD.resolve() == STATE.resolve():
            OLD.unlink()
            if RESTORING:
                if RETIRED_LEGACY is not None:
                    RETIRED_LEGACY.rename(OLD)
            else:
                STATE.rename(OLD)
                (OLD/'cutover.json').unlink(missing_ok=True)
        override = UNITS/'internet-map-api.service.d/coordination.conf'
        override.unlink(missing_ok=True)
        subprocess.run(['systemctl','daemon-reload'],capture_output=True)
        for unit, enabled in original_enabled.items():
            if enabled:
                subprocess.run(['systemctl','enable','--now',unit],capture_output=True)
        subprocess.run(['systemctl','restart','internet-map-api.service'],capture_output=True)
        raise


if __name__ == '__main__':
    install()

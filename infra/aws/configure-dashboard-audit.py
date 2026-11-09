#!/usr/bin/env python3
"""Install the shared dashboard identity key without changing login or tool access."""
import os
from pathlib import Path
import secrets
import tempfile


def atomic_write(path, data, mode=0o600):
    if path.is_symlink():
        raise RuntimeError('Refusing a symlink configuration path')
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix='.audit-', dir=path.parent)
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, 'wb') as output:
            output.write(data)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def configure(root=Path('/opt/cnc'), units=Path('/etc/systemd/system')):
    path = root / 'dashboard-audit.env'
    if path.is_symlink():
        raise RuntimeError('Refusing a symlink audit key')
    if path.exists():
        entries = dict(line.split('=', 1) for line in path.read_text().splitlines()
                       if '=' in line and not line.startswith('#'))
        secret = entries.get('CNC_DASHBOARD_AUDIT_SECRET', '')
        if len(secret) < 32 or any(c.isspace() for c in secret) or any(c in secret for c in "'\"\\"):
            raise RuntimeError('Existing dashboard audit key is invalid; repair explicitly')
        path.chmod(0o600)
    else:
        atomic_write(path, ('CNC_DASHBOARD_AUDIT_SECRET=' + secrets.token_hex(32) + '\n').encode())
    for service in ('cnc-server', 'cnc-ui'):
        atomic_write(units / (service + '.service.d') / 'zz-dashboard-audit.conf',
                     ('[Service]\nEnvironmentFile=' + str(path) + '\n').encode())


if __name__ == '__main__':
    configure()
    print('Shared dashboard audit key configured for controller and UI.')

"""Internet Map control plane; CNC owns execution, not discovery state."""
import json
import os
from pathlib import Path
import sqlite3
import urllib.request
from urllib.parse import urlsplit


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args):
        return None


class CNCClient:
    def __init__(self, origin, secret):
        parsed = urlsplit(origin)
        if (parsed.scheme not in ('http', 'https') or not parsed.hostname
                or parsed.username or parsed.password or parsed.query
                or parsed.fragment or parsed.path not in ('', '/') or len(secret) < 32):
            raise ValueError('Invalid private CNC connection')
        self.origin, self.secret = origin.rstrip('/'), secret.encode()
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def workers(self):
        with self.opener.open(self.origin + '/api/workers', timeout=15) as response:
            raw = response.read((4 << 20) + 1)
        if len(raw) > 4 << 20:
            raise ValueError('Worker response exceeds limit')
        workers = json.loads(raw)
        allowed = ('id', 'status', 'max_tasks', 'current_load', 'resources')
        return [{k: worker[k] for k in allowed if k in worker} for worker in workers]


class DiscoveryControl:
    def __init__(self, database, cnc=None):
        self.database = Path(database)
        self.root = self.database.parent / 'discovery'
        self.cnc = cnc

    def status(self):
        status = {}
        path = self.root / 'status.json'
        if path.exists():
            status = json.loads(path.read_text())
        state = self.root / 'generator-state.db'
        if state.exists():
            with sqlite3.connect(state.as_uri() + '?mode=ro', uri=True, timeout=10) as db:
                row = db.execute('SELECT available,target,refilling FROM pool_policy WHERE slot=1').fetchone()
                if row:
                    status.update(pool_available=row[0], pool_target=row[1], pool_refilling=bool(row[2]))
        return dict(status, owner='internet-map', executor='cnc',
                    scanning_enabled=(self.root / 'cutover.json').exists() and not (self.root / 'paused').exists(),
                    collection_while_paused=True, configured=self.cnc is not None)

    def control(self, action):
        if action not in ('pause', 'resume'):
            raise ValueError('Choose pause or resume')
        self.root.mkdir(parents=True, exist_ok=True, mode=0o700)
        if action == 'resume':
            if self.cnc is None or not (self.root / 'cutover.json').exists():
                raise ValueError('Discovery migration must be verified before scanning resumes')
            (self.root / 'paused').unlink(missing_ok=True)
        else:
            (self.root / 'paused').touch(mode=0o600, exist_ok=True)
        return self.status()


def service(database):
    origin = os.environ.get('INTERNET_MAP_CNC_URL', '')
    secret = os.environ.get('CNC_DASHBOARD_AUDIT_SECRET', '')
    return DiscoveryControl(Path(database).resolve(), CNCClient(origin, secret) if origin and secret else None)

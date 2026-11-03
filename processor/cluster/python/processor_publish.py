"""Publish selected UTF-8 text files from a processor job.

Call Publisher().publish(path, name="results.txt") after flushing complete lines.
The durable outbox survives transfer failures; the worker drains it on completion.
"""
from contextlib import contextmanager
import hashlib
import fcntl
import json
import os
from pathlib import Path
import sqlite3
import sys
import urllib.error
import urllib.request
import uuid

MAX_BYTES = 1 << 20


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, file, code, message, headers, newurl):
        return None


class Publisher:
    def __init__(self, outbox=None, url=None, token=None):
        self.outbox = Path(outbox or os.environ["CNC_PUBLICATION_OUTBOX"])
        self.url = url or os.environ["CNC_PUBLICATION_URL"]
        self.token = token or os.environ["CNC_PUBLICATION_TOKEN"]
        self.outbox.parent.mkdir(parents=True, exist_ok=True)
        with self.connect() as db:
            db.executescript("""
            CREATE TABLE IF NOT EXISTS cursors(path TEXT PRIMARY KEY, name TEXT NOT NULL,
              identity TEXT NOT NULL,generation TEXT NOT NULL,offset INTEGER NOT NULL,
              prefix_hash TEXT NOT NULL,sequence INTEGER NOT NULL);
            CREATE TABLE IF NOT EXISTS pending(id INTEGER PRIMARY KEY AUTOINCREMENT,body TEXT NOT NULL);
            """)

    @contextmanager
    def connect(self):
        db = sqlite3.connect(self.outbox, timeout=30)
        db.execute("PRAGMA journal_mode=WAL")
        db.execute("PRAGMA synchronous=FULL")
        try:
            with db:
                yield db
        finally:
            db.close()

    def publish(self, path, name=None, deliver=True):
        path = Path(path).absolute()
        name = name or path.name
        name = os.environ.get("CNC_PUBLICATION_PREFIX", "") + name
        if not name or name.startswith(".") or "/" in name or "\\" in name or name in (".", ".."):
            raise ValueError("publication name must be a visible filename")
        if path.is_symlink():
            raise ValueError("publication requires a regular file")
        # Parent directory aliases (e.g. macOS /var -> /private/var) must
        # share one cursor between a wrapper and the automatic monitor.
        path = path.resolve(strict=True)
        with self.connect() as db, path.open("rb") as source:
            db.execute("BEGIN IMMEDIATE")
            stat = os.fstat(source.fileno())
            import stat as stat_module
            if not stat_module.S_ISREG(stat.st_mode):
                raise ValueError("publication requires a regular file")
            identity = f"{stat.st_dev}:{stat.st_ino}"
            previous = db.execute("SELECT name,identity,generation,offset,prefix_hash,sequence FROM cursors WHERE path=?", (str(path),)).fetchone()
            generation, offset, sequence = uuid.uuid4().hex, 0, 0
            digest = hashlib.sha256()
            if previous and previous[0] == name and previous[1] == identity and previous[3] <= stat.st_size:
                remaining = previous[3]
                while remaining:
                    data = source.read(min(65536, remaining))
                    if not data:
                        break
                    digest.update(data)
                    remaining -= len(data)
                if not remaining and digest.hexdigest() == previous[4]:
                    generation, offset, sequence = previous[2], previous[3], previous[5]
                else:
                    digest = hashlib.sha256()
            source.seek(offset)
            lines, size = [], 0

            def enqueue():
                nonlocal lines, size, sequence
                if not lines:
                    return
                sequence += 1
                body = json.dumps({"name": name, "generation": generation, "sequence": sequence, "lines": lines}, ensure_ascii=False)
                db.execute("INSERT INTO pending(body) VALUES(?)", (body,))
                lines, size = [], 0

            # Only observe the current snapshot, and never acknowledge a partial line.
            while source.tell() < stat.st_size:
                data = source.readline(min(MAX_BYTES + 1, stat.st_size - source.tell()))
                if len(data) > MAX_BYTES:
                    raise ValueError("result line exceeds 1 MiB")
                if not data.endswith(b"\n"):
                    break
                if size + len(data) > MAX_BYTES or len(lines) == 1000:
                    enqueue()
                lines.append(data.decode("utf-8"))
                size += len(data)
                digest.update(data)
                offset += len(data)
            enqueue()
            db.execute("INSERT OR REPLACE INTO cursors VALUES(?,?,?,?,?,?,?)", (str(path), name, identity, generation, offset, digest.hexdigest(), sequence))
        return self.drain() if deliver else True

    def drain(self):
        # Serialize senders without holding a SQLite write lock across network I/O.
        # Capture continues during outages; repeated delivery is idempotent.
        with self.outbox.with_suffix(self.outbox.suffix + ".delivery.lock").open("a") as lock:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                return False
            with self.connect() as db:
                pending = db.execute("SELECT id,body FROM pending ORDER BY id").fetchall()
            for identifier, body in pending:
                request = urllib.request.Request(self.url, data=body.encode("utf-8"), headers={"Authorization": "Bearer " + self.token, "Content-Type": "application/json"}, method="POST")
                try:
                    with urllib.request.build_opener(NoRedirect()).open(request, timeout=5) as response:
                        receipt = json.load(response)
                        if not receipt.get("accepted"):
                            raise ValueError("publication was not acknowledged")
                except (OSError, urllib.error.URLError, ValueError) as error:
                    if isinstance(error, urllib.error.HTTPError):
                        error.close()
                    return False
                with self.connect() as db:
                    db.execute("DELETE FROM pending WHERE id=?", (identifier,))
        return True


def publish(path, name=None):
    return Publisher().publish(path, name)


if __name__ == "__main__":
    if sys.argv[1:] != ["--drain"]:
        raise SystemExit("Usage: python3 -m processor_publish --drain")
    raise SystemExit(0 if Publisher().drain() else 1)

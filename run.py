#!/usr/bin/env python3
"""Run the Internet Map application and private catalog API locally."""
import os
from pathlib import Path
import secrets
import signal
import subprocess
import sys
import time
from main import connect

root = Path(__file__).resolve().parent
env = dict(os.environ)
env["INTERNET_MAP_API_KEY"] = secrets.token_urlsafe(32)
env["INTERNET_MAP_API_URL"] = "http://127.0.0.1:3500"
db = connect(root / "data/catalog.db")
db.close()
children = []
def stop(signum, frame):
    raise KeyboardInterrupt
signal.signal(signal.SIGTERM, stop)
try:
    children.append(subprocess.Popen([sys.executable, str(root / "api.py"), "--database", str(root / "data/catalog.db")], cwd=root, env=env))
    children.append(subprocess.Popen(["npm", "run", "start"], cwd=root / "web", env=env, start_new_session=True))
    print("Internet Map: http://127.0.0.1:3501", flush=True)
    while all(child.poll() is None for child in children):
        time.sleep(0.5)
except KeyboardInterrupt:
    pass
finally:
    for child in children:
        if child.poll() is None:
            if child is children[-1] and len(children) == 2:
                os.killpg(child.pid, signal.SIGTERM)
            else:
                child.terminate()
    for child in children:
        try:
            child.wait(timeout=5)
        except subprocess.TimeoutExpired:
            child.kill()

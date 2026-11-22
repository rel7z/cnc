#!/usr/bin/env python3
"""Run the Internet Map application and private catalog API locally."""
import argparse
import os
from pathlib import Path
import secrets
import signal
import subprocess
import sys
import time

from main import connect

root = Path(__file__).resolve().parent

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dev", "-d", action="store_true", help="Run Next.js in hot-reload development mode (npm run dev)")
    parser.add_argument("--port", type=int, default=3501, help="Frontend port (default: 3501)")
    parser.add_argument("--api-port", type=int, default=3500, help="Catalog API port (default: 3500)")
    parser.add_argument("--database", type=Path, default=root / "data/catalog.db")
    args = parser.parse_args()

    env = dict(os.environ)
    env["INTERNET_MAP_API_KEY"] = secrets.token_urlsafe(32)
    env["INTERNET_MAP_API_URL"] = f"http://127.0.0.1:{args.api_port}"
    env["PORT"] = str(args.port)

    # Initialize tables if needed
    db = connect(args.database)
    db.close()

    web_dir = root / "web"
    if not args.dev and not (web_dir / ".next").exists():
        print("[*] Building Next.js production bundle first (run with --dev to skip build)...", flush=True)
        subprocess.run(["npm", "run", "build"], cwd=web_dir, check=True)

    mode_label = "Development (Hot Reload)" if args.dev else "Production (Optimized Build)"
    npm_cmd = ["npm", "run", "dev"] if args.dev else ["npm", "run", "start"]

    children = []
    def stop(signum, frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, stop)

    try:
        # Start Python catalog API
        api_proc = subprocess.Popen(
            [sys.executable, str(root / "api.py"), "--database", str(args.database), "--port", str(args.api_port)],
            cwd=root,
            env=env,
        )
        children.append(api_proc)

        # Start Next.js frontend
        web_proc = subprocess.Popen(
            npm_cmd,
            cwd=web_dir,
            env=env,
            start_new_session=True,
        )
        children.append(web_proc)

        banner = f"""
===================================================================
                  Internet Map - PublicWWW Intel & Catalog
===================================================================
  UI:          http://127.0.0.1:{args.port}
  Catalog API: http://127.0.0.1:{args.api_port}
  Mode:        {mode_label}
  Database:    {args.database.relative_to(root) if args.database.is_relative_to(root) else args.database}
===================================================================
"""
        print(banner, flush=True)

        while all(child.poll() is None for child in children):
            time.sleep(0.5)

    except KeyboardInterrupt:
        print("\n[*] Shutting down Internet Map services...", flush=True)
    finally:
        for child in children:
            if child.poll() is None:
                if child is children[-1] and len(children) == 2:
                    try:
                        os.killpg(child.pid, signal.SIGTERM)
                    except OSError:
                        pass
                else:
                    child.terminate()
        for child in children:
            try:
                child.wait(timeout=4)
            except subprocess.TimeoutExpired:
                child.kill()
        print("[+] All services stopped cleanly.", flush=True)


if __name__ == "__main__":
    main()

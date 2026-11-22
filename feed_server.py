#!/usr/bin/env python3
"""Feed a local dataset file directly into the DigitalOcean server catalog database."""
import argparse
import os
from pathlib import Path
import subprocess
import sys

SSH_CONFIG = Path.home() / "Documents/GitHub/cnc/.digitalocean/new-20261122/ssh_config"
HOST = "cnc-do-controller"
REMOTE_MAP_DIR = "/opt/internet-map"

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("file", type=Path, help="Local file to feed to server (txt, csv, jsonl, observations)")
    parser.add_argument("--format", choices=("txt", "csv", "jsonl", "observations"), default="txt")
    parser.add_argument("--source", default="", help="Label for this import batch (defaults to filename)")
    args = parser.parse_args()

    if not args.file.is_file():
        sys.exit(f"File not found: {args.file}")

    if not SSH_CONFIG.is_file():
        sys.exit(f"SSH config not found at: {SSH_CONFIG}")

    source_label = args.source or args.file.stem
    remote_filename = f"feed-{args.file.name}"
    remote_path = f"{REMOTE_MAP_DIR}/datasets/{remote_filename}"

    print(f"[*] Uploading {args.file} ({args.file.stat().st_size:,} bytes) to {HOST}:{remote_path}...", flush=True)
    scp_cmd = ["scp", "-F", str(SSH_CONFIG), str(args.file), f"{HOST}:{remote_path}"]
    subprocess.run(scp_cmd, check=True)

    print(f"[*] Importing into server database (/opt/internet-map/data/catalog.db)...", flush=True)
    ssh_cmd = [
        "ssh", "-F", str(SSH_CONFIG), HOST,
        f"cd {REMOTE_MAP_DIR} && python3 main.py import {remote_path} --format {args.format} --source '{source_label}'"
    ]
    subprocess.run(ssh_cmd, check=True)

    print(f"[*] Current server stats:", flush=True)
    stats_cmd = [
        "ssh", "-F", str(SSH_CONFIG), HOST,
        f"cd {REMOTE_MAP_DIR} && python3 main.py stats"
    ]
    subprocess.run(stats_cmd, check=True)
    print(f"\n[+] Success! Check the web UI at: http://127.0.0.1:3501", flush=True)

if __name__ == "__main__":
    main()

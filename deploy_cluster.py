#!/usr/bin/env python3
"""Deploy and manage Internet Map Distributed Worker swarm across DigitalOcean droplets."""
import argparse
import concurrent.futures
from pathlib import Path
import subprocess
import sys
import time

SSH_CONFIG = Path("/Users/fahrel/Documents/GitHub/cnc/.digitalocean/new-20261122/ssh_config")
CONTROLLER_PUB_KEY = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICT0p3DfEtpWUAZFqWJEjkSiMJL8xIsSxMVfkcAxbYsh root@cnc-do-controller"

SYSTEMD_UNIT = """[Unit]
Description=Internet Map Distributed Crawler Worker
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/internet-map-worker
ExecStart=/usr/bin/python3 /opt/internet-map-worker/cluster_worker.py --master http://10.104.0.2:3500 --concurrency 500 --batch-size 2000
Restart=on-failure
RestartSec=5s
KillMode=mixed
TimeoutStopSec=10s

# Hardware Isolation (Cgroups v2) - Never starve CNC worker
CPUWeight=50
CPUQuota=150%
MemoryMax=1000M
MemoryHigh=800M
Nice=5
TasksMax=1200
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
"""

WORKERS = [f"cnc-do-worker-{i}" for i in range(1, 9)]


def run_ssh(host: str, cmd: str, timeout: int = 30) -> tuple[int, str, str]:
    full_cmd = ["ssh", "-F", str(SSH_CONFIG), host, cmd]
    res = subprocess.run(full_cmd, capture_output=True, text=True, timeout=timeout)
    return res.returncode, res.stdout.strip(), res.stderr.strip()


def run_scp(host: str, local_path: Path, remote_path: str, timeout: int = 30) -> tuple[int, str, str]:
    full_cmd = ["scp", "-F", str(SSH_CONFIG), str(local_path), f"{host}:{remote_path}"]
    res = subprocess.run(full_cmd, capture_output=True, text=True, timeout=timeout)
    return res.returncode, res.stdout.strip(), res.stderr.strip()


def deploy_worker(host: str, worker_script_path: Path) -> dict:
    t0 = time.time()
    results = {"host": host, "status": "deploying"}

    # 1. Authorize controller key
    auth_cmd = (
        f'grep -q "{CONTROLLER_PUB_KEY.split()[-1]}" ~/.ssh/authorized_keys 2>/dev/null || '
        f'echo "{CONTROLLER_PUB_KEY}" >> ~/.ssh/authorized_keys'
    )
    code, out, err = run_ssh(host, auth_cmd)
    if code != 0:
        return {"host": host, "status": "failed", "step": "ssh_key", "error": err}

    # 2. Check / install python3-aiohttp
    chk_py = "python3 -c 'import aiohttp' 2>/dev/null"
    code, _, _ = run_ssh(host, chk_py)
    if code != 0:
        inst_cmd = (
            "DEBIAN_FRONTEND=noninteractive apt-get update -qq && "
            "DEBIAN_FRONTEND=noninteractive apt-get install -y -qq python3-aiohttp"
        )
        code, out, err = run_ssh(host, inst_cmd, timeout=120)
        if code != 0:
            return {"host": host, "status": "failed", "step": "install_aiohttp", "error": err}

    # 3. Create directory
    code, out, err = run_ssh(host, "mkdir -p /opt/internet-map-worker")
    if code != 0:
        return {"host": host, "status": "failed", "step": "mkdir", "error": err}

    # 4. Copy worker script
    code, out, err = run_scp(host, worker_script_path, "/opt/internet-map-worker/cluster_worker.py")
    if code != 0:
        return {"host": host, "status": "failed", "step": "scp_script", "error": err}

    # 5. Write systemd unit
    unit_b64 = SYSTEMD_UNIT.encode("utf-8").hex()
    unit_cmd = f"echo '{unit_b64}' | xxd -p -r > /etc/systemd/system/internet-map-worker.service && systemctl daemon-reload"
    code, out, err = run_ssh(host, unit_cmd)
    if code != 0:
        return {"host": host, "status": "failed", "step": "systemd_unit", "error": err}

    elapsed = time.time() - t0
    return {"host": host, "status": "ok", "elapsed": round(elapsed, 1)}


def control_worker(host: str, action: str) -> dict:
    if action == "status":
        code, out, _ = run_ssh(host, "systemctl is-active internet-map-worker 2>/dev/null || echo inactive")
        cgroup_out = ""
        if out == "active":
            _, cgroup_out, _ = run_ssh(
                host,
                "systemctl show internet-map-worker --property=CPUQuotaPerSecUSec,MemoryCurrent,TasksCurrent 2>/dev/null"
            )
        return {"host": host, "state": out, "details": cgroup_out.replace("\n", " ")}
    elif action in ("start", "stop", "restart"):
        code, out, err = run_ssh(host, f"systemctl {action} internet-map-worker")
        return {"host": host, "action": action, "success": (code == 0), "error": err}
    return {"host": host, "error": f"Unknown action {action}"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["deploy", "status", "start", "stop", "restart"])
    parser.add_argument("--workers", nargs="*", default=WORKERS, help="Target worker hosts")
    args = parser.parse_args()

    local_script = Path(__file__).resolve().parent / "cluster_worker.py"
    if not local_script.exists():
        print(f"Error: {local_script} does not exist.")
        sys.exit(1)

    print(f"[*] Executing '{args.action}' on {len(args.workers)} worker nodes...")

    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as ex:
        if args.action == "deploy":
            futures = [ex.submit(deploy_worker, w, local_script) for w in args.workers]
        else:
            futures = [ex.submit(control_worker, w, args.action) for w in args.workers]

        for fut in concurrent.futures.as_completed(futures):
            res = fut.result()
            print(f" -> {res['host']}: {res}")


if __name__ == "__main__":
    main()

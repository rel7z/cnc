#!/usr/bin/env python3
"""Package unrestricted CNC source, deploy controller, then seven EC2 workers."""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import tarfile
import time

from provision import ROOT, STATE_DIR, STATE_FILE, WORKERS, REGION, TYPE, WORKER_TYPE, CLUSTER, hourly_price, names


SOURCE_ROOTS = ("cnc-api", "cnc-backend", "cnc-ui", "infra/aws", "infra/upcloud")
PROCESSOR_ROOTS = ("api", "cmd", "internal", "cluster", "deploy")
SOURCE_SUFFIXES = {".go", ".mod", ".sum", ".ts", ".tsx", ".js", ".mjs", ".css",
                   ".json", ".svg", ".png", ".ico", ".md", ".sh", ".py", ".sql",
                   ".yaml", ".yml", ".html", ".service"}
SKIP_DIRS = {"node_modules", ".next", "__pycache__", ".git", "dist", "build"}


def included_files():
    selected = set()
    for root in SOURCE_ROOTS:
        base = ROOT / root
        for directory, children, files in os.walk(base, followlinks=False):
            children[:] = [child for child in children if child not in SKIP_DIRS
                           and not (Path(directory) / child).is_symlink()]
            for name in files:
                path = Path(directory) / name
                relative = path.relative_to(ROOT)
                if path.is_symlink() or name.startswith(".env") or name.endswith("_config.json"):
                    continue
                if name in {"server_config.json", "worker_config.json", "cnc_config.json",
                            "credentials.json", "token.json"}:
                    continue
                if path.suffix in SOURCE_SUFFIXES or name == "Makefile" or (
                        root == "cnc-ui" and "public" in relative.parts and path.suffix == ".txt"):
                    selected.add(relative)
    for directory in PROCESSOR_ROOTS:
        base = ROOT / "processor" / directory
        for path in base.rglob("*"):
            if path.is_file() and not path.is_symlink() and path.suffix in SOURCE_SUFFIXES:
                selected.add(path.relative_to(ROOT))
    for name in ("go.mod", "go.sum", "config.json"):
        selected.add(Path("processor") / name)
    # Distributed tests execute these harmless local-only fixture programs.
    for name in ("scanner.py", "scanner.go"):
        selected.add(Path("processor/examples/default-folder") / name)
    for relative in selected:
        if not (ROOT / relative).is_file():
            raise RuntimeError(f"Missing release input: {relative}")
        if relative.parts[:2] in (("processor", "tools"), ("docs", "processor")):
            raise RuntimeError(f"Protected path in release: {relative}")
    return sorted(selected)


def prepare():
    STATE_DIR.mkdir(mode=0o700, exist_ok=True)
    STATE_DIR.chmod(0o700)
    temp = STATE_DIR / "release-current.tar.gz"
    files = included_files()
    with tarfile.open(temp, "w:gz") as archive:
        for relative in files:
            archive.add(ROOT / relative, arcname=str(relative), recursive=False)
    revision = hashlib.sha256(temp.read_bytes()).hexdigest()
    final = STATE_DIR / f"release-{revision[:16]}.tar.gz"
    temp.replace(final)
    final.chmod(0o600)
    return final, revision, files


def run(command, *, input=None, capture=True):
    result = subprocess.run(command, input=input, text=True,
                            capture_output=capture, check=True)
    return result.stdout if capture else ""


def ssh(name, command, *, input=None, capture=True):
    return run(["ssh", "-F", str(STATE_DIR / "ssh_config"), name, command],
               input=input, capture=capture)


def access_data():
    path = STATE_DIR / "dashboard-access.json"
    if path.exists():
        return json.loads(path.read_text())
    data = {"username": "fahrel", "password": "fahrel",
            "session_secret": secrets.token_urlsafe(48), "url": "http://localhost:8081/dashboard"}
    path.write_text(json.dumps(data, indent=2) + "\n")
    path.chmod(0o600)
    return data


def encoded(data):
    return base64.b64encode(json.dumps(data, separators=(",", ":")).encode()).decode()


def deploy_server(archive, revision, state, access):
    server = state["nodes"]["cnc-aws-server"]
    run(["scp", "-F", str(STATE_DIR / "ssh_config"), str(archive),
         "cnc-aws-server:/tmp/cnc-source.tar.gz"], capture=False)
    setup = ("set -e\n"
             f"sudo mkdir -p /opt/cnc/releases/{revision[:16]}\n"
             f"sudo tar -xzf /tmp/cnc-source.tar.gz -C /opt/cnc/releases/{revision[:16]}\n"
             f"sudo ln -sfn /opt/cnc/releases/{revision[:16]} /opt/cnc/source\n")
    ssh("cnc-aws-server", "bash -s", input=setup, capture=False)
    cloud_config = {"region": REGION, "cluster": CLUSTER, "instance_type": TYPE,
                    "worker_instance_type": WORKER_TYPE,
                    "controller_private_ip": server["private_ip"],
                    "subnet_id": state["subnet_id"], "worker_security_group": state["worker_security_group"],
                    "key_name": state["key_name"], "ami": state["ami"],
                    "root_device": state["root_device"], "hourly_price_usd": hourly_price(),
                    "worker_hourly_price_usd": hourly_price(WORKER_TYPE)}
    payload = {"private_ip": server["private_ip"], "revision": revision,
               "login_user": access["username"], "login_password": access["password"],
               "session_secret": access["session_secret"], "aws_config": cloud_config}
    command = f"export CNC_DEPLOY_DATA={encoded(payload)}\nexec bash /opt/cnc/source/infra/aws/install-controller.sh\n"
    ssh("cnc-aws-server", "sudo bash -s", input=command, capture=False)


def deploy_worker(name, revision, state):
    payload = {"name": name, "private_ip": state["nodes"]["cnc-aws-server"]["private_ip"]}
    installer = (ROOT / "infra/aws/install-worker.sh").read_text()
    script = f"export CNC_DEPLOY_DATA={encoded(payload)}\n" + installer
    ssh(name, "sudo bash -s", input=script, capture=False)
    actual = ssh(name, "cat /opt/cnc/RELEASE").strip()
    if actual != revision:
        raise RuntimeError(f"Worker release mismatch on {name}: {actual}")


def verify(revision, state):
    server = state["nodes"]["cnc-aws-server"]
    for service in ("cnc-server", "cnc-processor", "cnc-ui"):
        if ssh("cnc-aws-server", f"systemctl is-active {service}").strip() != "active":
            raise RuntimeError(f"Controller service inactive: {service}")
    deadline = time.monotonic() + 180
    expected = set(state["nodes"]) - {"cnc-aws-server"}
    while True:
        raw = ssh("cnc-aws-server", f"curl -fsS http://{server['private_ip']}:8080/api/workers")
        online = {worker["id"] for worker in json.loads(raw)
                  if worker.get("status") in ("online", "busy")}
        if expected <= online:
            break
        if time.monotonic() >= deadline:
            raise RuntimeError(f"Only {len(expected & online)}/{len(expected)} workers registered")
        time.sleep(5)
    if ssh("cnc-aws-server", "cat /opt/cnc/RELEASE").strip() != revision:
        raise RuntimeError("Controller release mismatch")
    state.update(revision=revision, verified_workers=len(expected), workers_target=WORKERS,
                 verified_at=int(time.time()))
    temporary = STATE_FILE.with_suffix(".tmp")
    temporary.write_text(json.dumps(state, indent=2, sort_keys=True) + "\n")
    temporary.chmod(0o600)
    temporary.replace(STATE_FILE)
    print(json.dumps({"revision": revision, "workers_online": len(expected),
                      "dashboard": "http://localhost:8081/dashboard"}), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--deploy", action="store_true", help="Deploy to provisioned EC2 nodes")
    args = parser.parse_args()
    archive, revision, files = prepare()
    print(json.dumps({"archive": str(archive), "revision": revision,
                      "source_files": len(files), "protected_tools_included": False}), flush=True)
    if not args.deploy:
        return
    state = json.loads(STATE_FILE.read_text())
    count = len(state.get("nodes", {})) - 1
    if count < 1 or count > WORKERS or set(state["nodes"]) != set(names(count)):
        raise RuntimeError("Provision a controller and a contiguous worker set before deploying")
    access = access_data()
    deploy_server(archive, revision, state, access)
    for name in names(count)[1:]:
        deploy_worker(name, revision, state)
    verify(revision, state)


if __name__ == "__main__":
    main()

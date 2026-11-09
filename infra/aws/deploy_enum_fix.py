#!/usr/bin/env python3
import json
import subprocess
import sys
import time

PROFILE = "cnc"
REGION = "us-east-2"
BUCKET = "cnc-release-373384938779-45e28bc4ac"
EXPECTED_SHA = "d4bf2ab363ebf5360b941b37464e26e911dcfbfdf83287cdf3b45e9bca4e1742"
CONTROLLER_ID = "i-0780348116e70e190"
WORKER_IDS = ["i-0fcfa71d3663e15ae", "i-0c6ae9262423edc0b", "i-024d40c7fc4583647"]

def run_cmd(cmd):
    res = subprocess.run(cmd, capture_output=True, text=True)
    if res.returncode != 0:
        raise RuntimeError(f"Command failed ({res.returncode}): {' '.join(cmd)}\n{res.stderr}")
    return res.stdout.strip()

def send_ssm(instance_id, script, label):
    cmd = [
        "aws", "ssm", "send-command",
        "--instance-ids", instance_id,
        "--document-name", "AWS-RunShellScript",
        "--parameters", json.dumps({"commands": [script]}),
        "--profile", PROFILE,
        "--region", REGION,
        "--output", "json"
    ]
    out = json.loads(run_cmd(cmd))
    command_id = out["Command"]["CommandId"]
    print(f"[{label}] Sent SSM command {command_id} to {instance_id}")

    # Wait for completion
    while True:
        poll_cmd = [
            "aws", "ssm", "get-command-invocation",
            "--command-id", command_id,
            "--instance-id", instance_id,
            "--profile", PROFILE,
            "--region", REGION,
            "--output", "json"
        ]
        poll_res = json.loads(run_cmd(poll_cmd))
        status = poll_res["Status"]
        if status in ("Pending", "InProgress", "Delayed"):
            time.sleep(2)
            continue
        if status != "Success":
            raise RuntimeError(f"[{label}] Command failed with status {status}:\nStderr: {poll_res.get('StandardErrorContent')}\nStdout: {poll_res.get('StandardOutputContent')}")
        print(f"[{label}] Success: {poll_res.get('StandardOutputContent', '').strip()}")
        return poll_res.get("StandardOutputContent", "")

def main():
    print("Generating presigned URL for cnc-enum-linux...")
    presign_cmd = [
        "aws", "s3", "presign",
        f"s3://{BUCKET}/releases/cnc-enum-linux",
        "--expires-in", "3600",
        "--profile", PROFILE,
        "--region", REGION
    ]
    url = run_cmd(presign_cmd)

    controller_script = f"""
set -e
URL='{url}'
EXPECTED='{EXPECTED_SHA}'

curl -fsSL "$URL" -o /opt/cnc/tools/cnc-enum.new
CALC=$(sha256sum /opt/cnc/tools/cnc-enum.new | cut -d' ' -f1)
if [ "$CALC" != "$EXPECTED" ]; then
    echo "Checksum mismatch: $CALC vs $EXPECTED"
    exit 1
fi
chmod 0755 /opt/cnc/tools/cnc-enum.new
cp /opt/cnc/tools/cnc-enum /opt/cnc/tools/cnc-enum.bak
mv /opt/cnc/tools/cnc-enum.new /opt/cnc/tools/cnc-enum

TMPDIR=$(mktemp -d)
tar -xzf /opt/cnc/worker-tools.tar.gz -C "$TMPDIR"
cp /opt/cnc/tools/cnc-enum "$TMPDIR/tools/cnc-enum"
tar -czf /opt/cnc/worker-tools.tar.gz.new -C "$TMPDIR" tools node-runtime node-path 2>/dev/null || tar -czf /opt/cnc/worker-tools.tar.gz.new -C "$TMPDIR" .
cp /opt/cnc/worker-tools.tar.gz /opt/cnc/worker-tools.tar.gz.bak
mv /opt/cnc/worker-tools.tar.gz.new /opt/cnc/worker-tools.tar.gz
rm -rf "$TMPDIR"

echo "Controller updated: $(sha256sum /opt/cnc/tools/cnc-enum)"
"""

    print("Deploying to Controller...")
    send_ssm(CONTROLLER_ID, controller_script, "Controller")

    worker_script = f"""
set -e
URL='{url}'
EXPECTED='{EXPECTED_SHA}'

curl -fsSL "$URL" -o /opt/cnc/tools/cnc-enum.new
CALC=$(sha256sum /opt/cnc/tools/cnc-enum.new | cut -d' ' -f1)
if [ "$CALC" != "$EXPECTED" ]; then
    echo "Checksum mismatch: $CALC vs $EXPECTED"
    exit 1
fi
chmod 0755 /opt/cnc/tools/cnc-enum.new
cp /opt/cnc/tools/cnc-enum /opt/cnc/tools/cnc-enum.bak
mv /opt/cnc/tools/cnc-enum.new /opt/cnc/tools/cnc-enum

echo "Worker updated: $(sha256sum /opt/cnc/tools/cnc-enum)"
"""

    for i, wid in enumerate(WORKER_IDS, start=1):
        print(f"Deploying to Worker {i} ({wid})...")
        send_ssm(wid, worker_script, f"Worker-{i}")

    print("All nodes updated with new cnc-enum binary successfully!")

if __name__ == "__main__":
    main()

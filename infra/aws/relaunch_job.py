#!/usr/bin/env python3
import json
import subprocess
import time

PROFILE = "cnc"
REGION = "us-east-2"
CONTROLLER_ID = "i-0780348116e70e190"
WORKER_IDS = ["i-0fcfa71d3663e15ae", "i-0c6ae9262423edc0b", "i-024d40c7fc4583647"]
OLD_JOB = "enum_b501189e3d71cacf28e666e0"

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
        print(f"[{label}] Success:\n{poll_res.get('StandardOutputContent', '').strip()}")
        return poll_res.get("StandardOutputContent", "")

def main():
    # 1. Kill old running cnc-enum processes on all workers
    kill_script = "pkill -f /opt/cnc/tools/cnc-enum || true"
    for i, wid in enumerate(WORKER_IDS, start=1):
        print(f"Stopping old enum processes on Worker {i}...")
        send_ssm(wid, kill_script, f"Worker-{i}-kill")

    # 2. Cancel old job and relaunch on controller
    controller_script = f"""python3 - <<'EOF'
import hashlib, hmac, json, time, urllib.request
from pathlib import Path

root = Path('/opt/cnc')
config = json.loads((root / 'config.json').read_text())
base = 'http://' + config['http_addr']
opener = urllib.request.build_opener(urllib.request.ProxyHandler({{}}))

old_job = '{OLD_JOB}'
state_dir = Path(config['data_dir']) / 'enum-jobs' / old_job
state = json.loads((state_dir / 'state.json').read_text())
artifact = json.loads((state_dir / state['artifact_filename']).read_text())

# Cancel old job
try:
    cancel_req = urllib.request.Request(base + f'/api/jobs/{{old_job}}/cancel', b'', method='POST')
    opener.open(cancel_req, timeout=30)
    print(f"Old job {{old_job}} cancelled.")
except Exception as e:
    print(f"Cancel note: {{e}}")

old = json.load(opener.open(base + f'/api/jobs/{{old_job}}', timeout=30))
settings = old['enum']
request_data = {{
    'tool_id': 'enum',
    'input_file': old['input_file'],
    'mode': 'spread',
    'timeout_seconds': old['timeout_seconds'],
    'version_rules': artifact['rules'],
    'options': {{
        'threads': settings['threads'],
        'auto_concurrency': settings['automatic'],
        'timeout': settings['request_timeout_ms'] // 1000
    }}
}}
for name in settings.get('wordlists', []):
    if (state_dir / name).exists():
        request_data[name.removesuffix('.txt')] = (state_dir / name).read_text()

body = json.dumps(request_data).encode()
actor = settings.get('actor', 'fahrel')
secret = dict(line.split('=', 1) for line in (root / 'dashboard-audit.env').read_text().splitlines() if '=' in line)['CNC_DASHBOARD_AUDIT_SECRET']
stamp = str(int(time.time() * 1000))
sig_payload = actor + '\\n' + stamp + '\\nPOST\\n/api/tools/launch\\n' + hashlib.sha256(body).hexdigest()
signature = hmac.new(secret.encode(), sig_payload.encode(), hashlib.sha256).hexdigest()

req = urllib.request.Request(
    base + '/api/tools/launch',
    body,
    {{
        'Content-Type': 'application/json',
        'X-CNC-Actor': actor,
        'X-CNC-Audit-Time': stamp,
        'X-CNC-Audit-Signature': signature
    }}
)
with opener.open(req, timeout=600) as response:
    created = json.load(response)

print("RELAUNCH_RESULT:" + json.dumps(created))
EOF
"""
    print("Cancelling old job and relaunching new enum job on controller...")
    out = send_ssm(CONTROLLER_ID, controller_script, "Controller-Relaunch")

    new_job_id = None
    for line in out.splitlines():
        if "RELAUNCH_RESULT:" in line:
            data = json.loads(line.split("RELAUNCH_RESULT:")[1])
            new_job_id = data.get("job_id")
            break

    if not new_job_id:
        print("Failed to parse new job ID!")
        sys.exit(1)

    print(f"Successfully launched replacement job: {new_job_id}")

if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Deploy the pinned enum repair to existing idle workers with per-node rollback."""
import concurrent.futures
import json
from pathlib import Path
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
SHA = sys.argv[1]
assert len(SHA) == 64 and all(c in '0123456789abcdef' for c in SHA)
state = json.loads((ROOT / '.aws-cnc/state.json').read_text())
script = (ROOT / 'infra/aws/install-plugin-verification-worker.py').read_text().replace('EXPECTED_SHA', repr(SHA))
aws_cli = str(Path.home() / '.local/bin/aws')


def aws(*args):
    result = subprocess.run([aws_cli, *args, '--profile', 'cnc', '--region', state['region'], '--output', 'json'],
                            capture_output=True, text=True, check=True)
    return json.loads(result.stdout)


nodes = {k: v['id'] for k, v in state['nodes'].items() if k.startswith('cnc-aws-worker-')}
command_id = aws('ssm', 'send-command', '--instance-ids', *nodes.values(), '--document-name', 'AWS-RunShellScript',
                 '--comment', 'Approved enum identity/rule repair and idle worker capacity increase',
                 '--parameters', json.dumps({'commands': ["python3 - <<'WORKER_RELEASE'\n" + script + '\nWORKER_RELEASE\n'],
                                              'executionTimeout': ['240']}))['Command']['CommandId']
print(json.dumps({'ssm_command': command_id}), flush=True)


def amazon(item):
    name, instance = item
    while True:
        try:
            result = aws('ssm', 'get-command-invocation', '--command-id', command_id, '--instance-id', instance)
        except subprocess.CalledProcessError:
            time.sleep(2)
            continue
        if result['Status'] not in ('Pending', 'InProgress', 'Delayed'):
            break
        time.sleep(3)
    if result['Status'] != 'Success':
        return {'worker': name, 'ok': False, 'error': result.get('StandardErrorContent', '')[-1000:]}
    return dict(ok=True, **json.loads(result['StandardOutputContent'].strip().splitlines()[-1]))


def linode(number):
    name = f'cnc-linode-worker-{number}'
    result = subprocess.run(['ssh', '-F', str(ROOT / '.linode/cross-cloud-ssh_config'), '-o', 'StrictHostKeyChecking=yes',
                             name, 'python3', '-'], input=script, text=True, capture_output=True, timeout=240)
    if result.returncode:
        return {'worker': name, 'ok': False, 'error': result.stderr[-1000:]}
    return dict(ok=True, **json.loads(result.stdout.strip().splitlines()[-1]))


with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
    futures = [pool.submit(amazon, item) for item in nodes.items()] + [pool.submit(linode, i) for i in range(1, 9)]
    results = [future.result() for future in futures]
path = ROOT / '.aws-cnc/plugin-verification-workers.json'
path.write_text(json.dumps(results, indent=2)+'\n')
path.chmod(0o600)
print(json.dumps(results, indent=2))
raise SystemExit(0 if len(results) == 11 and all(r['ok'] for r in results) else 1)

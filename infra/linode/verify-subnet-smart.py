#!/usr/bin/env python3
"""Run an IPv4-only loopback fixture through all six production workers."""
import http.cookiejar
import json
from pathlib import Path
import time
import urllib.request
import uuid

BASE = 'http://127.0.0.1:3000'
OPENER = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
EXPECTED_SHA = '4d140062e1cb0e277cf34b373584ced32f515b260702477ab5686aaf8442576f'


def read_env(path):
    values = {}
    for line in Path(path).read_text().splitlines():
        key, _, value = line.partition('=')
        values[key] = value.strip('"')
    return values


def api(path, body=None):
    request = urllib.request.Request(BASE + path,
        data=None if body is None else json.dumps(body).encode(),
        headers={'Content-Type': 'application/json', 'Origin': BASE})
    return json.load(OPENER.open(request, timeout=20))


def cluster():
    env = read_env('/opt/cnc/processor.env')
    url = env['CNC_PROCESSOR_BROKER_URL'].rstrip('/') + '/api/processor/cluster/workers'
    request = urllib.request.Request(url, headers={'Authorization': 'Bearer ' + env['CNC_PROCESSOR_TOKEN']})
    return json.load(urllib.request.urlopen(request, timeout=15))


def main():
    login = read_env('/opt/cnc/login.env')
    api('/api/auth/login', {'username': login['CNC_LOGIN_USERNAME'], 'password': login['CNC_LOGIN_PASSWORD']})
    before = cluster()
    assert len(before) == 6 and all(w['status'] == 'online' for w in before), 'Expected six online workers'
    assert all(w['current_load'] == 0 for w in before), 'Wait for existing work before running the fixture'

    tag = 'subnet-ipv4-fixture-' + uuid.uuid4().hex
    output_name = tag + '.txt'
    command = "printf '%s\\n' '127.0.0.0/30' '127.0.0.0/30' '::/0' > subnet-fixture-input.txt; " \
        "/usr/bin/time -f '%M' -o subnet-fixture-rss.txt ./tools/subnet-finder " \
        "-l subnet-fixture-input.txt -out subnet-fixture-output.txt -c 100 -timeout 200ms " \
        ">/dev/null 2>subnet-fixture-stderr.txt; code=$?; " \
        "test \"$code\" -eq 0 || exit \"$code\"; " \
        "printf 'SUBNET_FIXTURE|%s|%s|%s\\n' \"$(hostname)\" \"$(sha256sum ./tools/subnet-finder | cut -d' ' -f1)\" \"$(cat subnet-fixture-rss.txt)\"; " \
        "cat subnet-fixture-output.txt"
    job = api('/api/jobs', {'name': 'Owned IPv4 subnet-finder fixture', 'mode': 'broadcast',
        'command': command, 'output_file': output_name, 'workers': 6, 'timeout_seconds': 30})
    job_id = job.get('id') or job['job_id']

    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        current = api('/api/jobs/' + job_id)
        if current['status'] in ('completed', 'failed', 'cancelled'):
            break
        time.sleep(1)
    else:
        raise RuntimeError('Loopback fixture job did not finish: ' + job_id)
    tasks = api('/api/jobs/' + job_id + '/tasks')
    assert current['status'] == 'completed', {'status': current['status'], 'job_id': job_id}
    assert len(tasks) == 6 and all(t['status'] == 'completed' and t.get('retry_count', 0) == 0 for t in tasks), 'Not all fixture tasks completed cleanly'
    assert len({t['assigned_to'] for t in tasks}) == 6, 'Fixture did not use six distinct workers'

    task_outputs = [((t.get('result') or {}).get('stdout') or '').splitlines() for t in tasks]
    assert all((t.get('result') or {}).get('exit_code') == 0 for t in tasks), 'Fixture command failed'
    proofs = [lines[0].split('|') for lines in task_outputs if lines and lines[0].startswith('SUBNET_FIXTURE|')]
    assert len(proofs) == 6, 'Expected one proof from each worker'
    assert all(p[2] == EXPECTED_SHA for p in proofs), 'A worker is running a different binary'
    rss = [int(p[3]) for p in proofs]
    results_by_task = [lines[1:] for lines in task_outputs]
    assert all(sorted(results) == ['127.0.0.1', '127.0.0.2'] for results in results_by_task), 'Unexpected, duplicate, or non-IPv4 fixture output'
    after = cluster()
    assert len(after) == 6 and all(w['status'] == 'online' and w['current_load'] == 0 for w in after), 'Worker health did not recover after fixture'

    print(json.dumps({'job_id': job_id, 'workers_verified': 6, 'worker_ids': sorted(t['assigned_to'] for t in tasks),
        'binary_sha256': EXPECTED_SHA, 'fixture_results_per_worker': ['127.0.0.1', '127.0.0.2'],
        'max_rss_kb_by_worker': sorted(rss),
        'all_tasks_completed': True, 'retries': 0, 'ipv6_input_ignored': True,
        'duplicate_input_deduplicated': True, 'all_workers_online_idle': True,
        'public_targets_used': False, 'private_tools_accessed': False}))


if __name__ == '__main__':
    main()

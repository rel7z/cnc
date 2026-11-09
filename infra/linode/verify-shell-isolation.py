#!/usr/bin/env python3
"""Run on the controller. Uses shell builtins only, never private tools."""
import http.cookiejar
import json
from pathlib import Path
import time
import urllib.request
import uuid

base = 'http://127.0.0.1:3000'
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
login = {}
for line in Path('/opt/cnc/login.env').read_text().splitlines():
    key, _, value = line.partition('=')
    login[key] = value.strip('"')


def api(path, body=None):
    request = urllib.request.Request(base+path,
        data=None if body is None else json.dumps(body).encode(),
        headers={'Content-Type': 'application/json', 'Origin': base})
    return json.load(opener.open(request, timeout=20))


api('/api/auth/login', {'username': login['CNC_LOGIN_USERNAME'], 'password': login['CNC_LOGIN_PASSWORD']})
tag = 'shell-isolation-'+uuid.uuid4().hex
fixture = Path('/opt/cnc/data/shell-isolation-fixtures')/tag
fixture.mkdir(parents=True)
source = fixture/'input.txt'
expected = [tag+'-'+str(i) for i in range(6)]
source.write_text('\n'.join(expected)+'\n')
output_name = tag+'.txt'
config = json.loads(Path('/opt/cnc/config.json').read_text())
watch = Path(config['gdrive']['watch_dir']).expanduser()
output = watch/output_name
assert not output.exists()


def launch(command, mode='spread'):
    response = api('/api/jobs', {'name': 'Owned shell isolation fixture', 'mode': mode,
        'command': command, 'input_file': str(source) if mode == 'spread' else '',
        'output_file': output_name, 'workers': 6, 'timeout_seconds': 15})
    return {'id': response.get('id') or response['job_id']}


def wait(job):
    deadline = time.monotonic()+60
    while time.monotonic() < deadline:
        current = api('/api/jobs/'+job['id'])
        if current['status'] in ('completed', 'failed', 'cancelled'):
            tasks = api('/api/jobs/'+job['id']+'/tasks')
            assert current['status'] == 'completed', {'status': current['status']}
            assert len(tasks) == 6 and all(t['status'] == 'completed' for t in tasks)
            assert len({t['assigned_to'] for t in tasks}) == 6
            return tasks
        time.sleep(1)
    raise RuntimeError('Fixture did not finish; inspect job '+job['id'])


command = ('case "$HOME" in */shell-runs/task-*) ;; *) exit 20;; esac; '
    'test "$HOME" = "$PWD" && test ! -e "$HOME/x.txt" && '
    'test ! -e '+output_name+' && cp {input} x.txt && '
    'cat x.txt > '+output_name+' && sleep 2 && cat x.txt')
first = launch(command)
tasks = wait(first)
assert sorted(output.read_text().splitlines()) == sorted(expected), 'Cross-task or duplicate output'
output.unlink()  # Only this verifier's own unique merged output.
second = launch('test ! -e x.txt && test ! -e '+output_name, mode='broadcast')
wait(second)
assert not output.exists(), 'Empty tasks replayed prior worker output'
print(json.dumps({'first_job': first['id'], 'empty_job': second['id'], 'workers_verified': 6,
    'fresh_home': True, 'same_filename_isolation': True, 'no_duplicate_stream': True,
    'empty_run_did_not_replay_results': True, 'private_tools_executed': False}))

#!/usr/bin/env python3
"""Run on the CNC server after deployment. Uses only isolated fixture input."""
import json, sqlite3, subprocess, time, uuid, urllib.request, urllib.error, http.cookiejar
from pathlib import Path

app = Path('/opt/cnc-processor')
config_path = app / 'config.json'
original_config = config_path.read_bytes()
settings = {}
for line in Path('/opt/cnc/processor.env').read_text().splitlines():
    key, _, value = line.partition('=')
    settings[key] = value.strip('"')
token = settings['CNC_PROCESSOR_TOKEN']
watch = Path(settings['CNC_WATCH_DIR'])
name = 'integration-' + uuid.uuid4().hex
filename = name + '.log'
source = watch / filename
fixture = app / 'tools' / (name + '.py')
fixture_id = 'tools/' + fixture.name

opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

def request(path, body=None, method=None, direct=False, authenticated=True):
    headers = {'Content-Type':'application/json', 'Origin':'http://127.0.0.1:3000'}
    if direct and authenticated:
        headers['Authorization'] = 'Bearer ' + token
    base = 'http://127.0.0.1:9191' if direct else 'http://127.0.0.1:3000'
    req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers, method=method)
    with opener.open(req, timeout=30) as response:
        return json.load(response)

def runs():
    with sqlite3.connect(app / 'processor.db', timeout=15) as db:
        return db.execute('SELECT r.id,r.status FROM runs r JOIN batches b ON b.acceptance_id=r.batch_acceptance_id WHERE b.filename=?', (filename,)).fetchall()

login={}
for line in Path('/opt/cnc/login.env').read_text().splitlines():
    k,_,v=line.partition('=');login[k]=v
request('/api/auth/login',{'username':login['CNC_LOGIN_USERNAME'],'password':login['CNC_LOGIN_PASSWORD']},'POST')
initial = request('/api/processor/health')
assert initial['status'] == 'ok'
try:
    request('/v1/health', direct=True, authenticated=False)
    raise AssertionError('processor allowed unauthenticated access')
except urllib.error.HTTPError as e:
    assert e.code == 401
run_ids = []
try:
    request('/api/processor/processing-state', {'enabled':False}, 'PUT')
    fixture.write_bytes(Path('/opt/cnc/source/processor/tools/test_program.py').read_bytes())
    config = json.loads(original_config)
    config.setdefault('routes', {})[filename] = ['tools/test_program.py', fixture_id]
    config.setdefault('export_results', []).append(fixture_id)
    if 'tools/test_program.py' not in config['export_results']:
        config['export_results'].append('tools/test_program.py')
    config_path.write_text(json.dumps(config))
    source.write_text(name)
    time.sleep(3)
    assert runs() == [], 'paused processor captured fixture'
    request('/api/processor/processing-state', {'enabled':True}, 'PUT')
    try:
        request('/v1/capture', {'filename':filename,'directory':str(watch)}, 'POST', direct=True)
        raise AssertionError('capture acknowledged incomplete line')
    except urllib.error.HTTPError as e:
        assert e.code == 409
    with source.open('a') as f:
        f.write('\n')
    assert request('/v1/capture', {'filename':filename,'directory':str(watch)}, 'POST', direct=True)['captured']
    source.unlink()  # Deliberately remove after durable acknowledgment.
    deadline = time.monotonic()+45
    while time.monotonic()<deadline:
        found = runs()
        if len(found)==2 and all(status=='completed' for _,status in found):
            break
        if any(status in ('failed','interrupted') for _,status in found):
            raise AssertionError(found)
        time.sleep(1)
    else:
        raise AssertionError('fixture timeout: '+str(runs()))
    run_ids = [rid for rid,_ in found]
    for rid in run_ids:
        output = watch / ('results_'+rid+'.txt')
        assert output.exists() and 'batch of 1 lines' in output.read_text(), 'fixture export missing'
    request('/api/processor/processing-state', {'enabled':False}, 'PUT')
    subprocess.run(['systemctl','restart','cnc-processor'], check=True)
    deadline=time.monotonic()+20
    while True:
        try:
            state=request('/api/processor/health')
            assert state['enabled'] is False
            break
        except (urllib.error.URLError, ConnectionError):
            if time.monotonic()>deadline: raise
            time.sleep(1)
    assert runs()==found, 'restart changed fixture results'
    workers=request('/api/workers')
    online=[w['id'] for w in workers if w['status'] in ('online','busy')]
    assert len(online)>=6, 'existing workers missing: '+str(online)
    print(json.dumps({'processor_auth':'verified','dashboard_controls':'verified','partial_line_retained':True,'durable_capture_before_deletion':True,'independent_fixture_runs':2,'outputs_verified':2,'pause_survives_restart':True,'workers_online':len(online),'fixture_run_ids':run_ids}))
finally:
    config_path.write_bytes(original_config)
    source.unlink(missing_ok=True)
    fixture.unlink(missing_ok=True)
    # Only remove the outputs created by this exact verification fixture.
    for rid in run_ids:
        (watch / ('results_'+rid+'.txt')).unlink(missing_ok=True)
    request('/api/processor/processing-state', {'enabled':initial['enabled']}, 'PUT')

#!/usr/bin/env python3
"""Trigger approved private lab inputs and verify worker artifacts in Google Drive."""
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
REMOTE = r"""
import hashlib, json, time, urllib.parse, urllib.request
from pathlib import Path
env = dict(line.split('=',1) for line in Path('/opt/cnc/processor.env').read_text().splitlines()
           if '=' in line and not line.startswith('#'))
def api(path):
    request = urllib.request.Request('http://127.0.0.1:9191/v1/'+path,
        headers={'Authorization':'Bearer '+env['CNC_PROCESSOR_TOKEN'].strip('"')})
    return json.load(urllib.request.urlopen(request,timeout=10))
before = {run['ID'] for run in api('runs') or []}
root = Path('/root/merged')
policy=json.loads(Path('/opt/cnc/processor-targets.json').read_text())
ftp_target=next(t for t in policy['ftp'] if t['url']=='ftp://192.168.145.81:2121')
inputs = {
    'processor-wordpress-authorized.txt':'http://192.168.145.81:18081\nftp://invalid-wordpress-input.example.invalid\n',
    'processor-ftp-authorized.txt':ftp_target['url']+' ('+ftp_target['username']+':'+ftp_target['password']+')\nftp://198.51.100.1\n',
}
for name, text in inputs.items():
    path = root/name
    if path.exists():
        with path.open('a') as out:
            out.write(text)
    else:
        temporary = root/('.lab-'+name)
        temporary.write_text(text)
        temporary.replace(path)
for _ in range(45):
    runs = [run for run in api('runs') or [] if run['ID'] not in before]
    if len(runs) >= 2 and all(run['Status'] == 'completed' for run in runs):
        break
    if any(run['Status'] in ('failed','interrupted') for run in runs):
        raise RuntimeError('Lab runs failed: '+json.dumps(runs))
    time.sleep(1)
else:
    raise RuntimeError('Lab runs not completed yet: '+json.dumps(runs))
results=[]
for run in runs:
    prefix = 'ftp_authorized_results_' if run['ProgramID'].endswith('ftp_checker.py') else 'wordpress_authorized_results_'
    filename=prefix+run['ID']+'.txt'
    artifact=Path('/opt/cnc-processor/distributed/attempts')/run['AttemptID']/'artifacts'/filename
    content=artifact.read_bytes()
    if run['ProgramID'].endswith('ftp_checker.py'):
        target=next(t for t in policy['ftp'] if t['url']=='ftp://192.168.145.81:2121')
        expected=target['url']+' — success login '+target['username']+':'+target['password']+'\n'
    else:
        expected='http://192.168.145.81:18081/ (admin:pass)\n'
    if content.decode() != expected:
        raise RuntimeError('Expected verified plain-text lab login for '+filename+'; contents withheld')
    results.append({'filename':filename,'sha256':hashlib.sha256(content).hexdigest(),
                    'verified_logins':1,'format':'plain_text'})
cfg=json.loads(Path('/opt/cnc/config.json').read_text())['gdrive']
token=json.loads(Path(cfg['token_path']).read_text())
form=urllib.parse.urlencode({'client_id':cfg['client_id'],'client_secret':cfg['client_secret'],
    'refresh_token':token['refresh_token'],'grant_type':'refresh_token'}).encode()
fresh=json.load(urllib.request.urlopen(urllib.request.Request('https://oauth2.googleapis.com/token',data=form),timeout=15))
def drive(path):
    req=urllib.request.Request('https://www.googleapis.com/drive/v3/'+path,
        headers={'Authorization':'Bearer '+fresh['access_token']})
    return urllib.request.urlopen(req,timeout=15).read()
for result in results:
    query="trashed = false and name = '"+result['filename']+"'"
    if cfg.get('parent_folder_id'):
        query+=" and '"+cfg['parent_folder_id']+"' in parents"
    params=urllib.parse.urlencode({'q':query,'fields':'files(id,name,webViewLink)','pageSize':100})
    for _ in range(20):
        matches=json.loads(drive('files?'+params))['files']
        verified=None
        for match in matches:
            content=drive('files/'+match['id']+'?alt=media')
            if hashlib.sha256(content).hexdigest()==result['sha256']:
                verified=match
                break
        if verified:
            result['drive']=verified
            result['drive_content_verified']=True
            break
        time.sleep(2)
    else:
        raise RuntimeError('Drive upload not verified for '+result['filename'])
report={'verified_at_utc':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),
        'release':Path('/opt/cnc-processor/AUTHORIZED_RELEASE').read_text().strip(),
        'health':api('health'),'runs':runs,'results':results,'cluster':api('cluster')}
print(json.dumps(report,indent=2))
"""

if __name__ == "__main__":
    response = subprocess.check_output(["ssh","-F",str(ROOT/".linode/ssh_config"),
                                       "cnc-linode-server","python3 -"], input=REMOTE.encode())
    destination=ROOT/"processor/authorized-lab-results.json"
    destination.write_bytes(response)
    import json
    report=json.loads(response)
    print("Plain-text results verified; credentials omitted from this report.")
    for result in report['results']:
        print(result['filename']+": "+result['drive'].get('webViewLink',result['drive']['id']))
    print("Saved report:",destination)

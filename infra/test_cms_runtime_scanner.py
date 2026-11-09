#!/usr/bin/env python3
"""Verify the real scanner executable against isolated loopback dummy sites."""
import argparse,hashlib,json,subprocess,tempfile,threading
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
def main():
 parser=argparse.ArgumentParser(description=__doc__)
 parser.add_argument('--binary',type=Path,required=True)
 parser.add_argument('--report',type=Path,required=True)
 parser.add_argument('--checks',type=Path,default=ROOT/'processor/internal/store/default-version-checks.json')
 args=parser.parse_args()
 checks=json.loads(args.checks.read_text())
 assert len(checks)==7
 cases=[]
 def add(name,php=None,core=None,plugin=None,want=(),status=200,headers=None,body=None):
  cases.append(dict(name=name,php=php,core=core,plugin=plugin,want=want,status=status,headers=headers,body=body))
 for lo,hi,last in [('8.5.0','8.5.12','8.5.11'),('8.4.0','8.4.27','8.4.26'),('8.3.0','8.3.36','8.3.35'),('8.2.0','8.2.35','8.2.34')]:
  for value in [lo,last,hi]:add('php-'+value,php=value,want=[('php:php-src',value)] if value!=hi else [])
 for value in ['6.9.0','6.9.4','6.9.5','7.0.0','7.0.1','7.0.2']:
  add('wp-'+value,core=value,want=[] if value in ['6.9.5','7.0.2'] else [('wordpress:core',value)])
 for value in ['3.3.0','3.3.1','3.3.2']:
  add('translatepress-'+value,plugin=value,want=[] if value=='3.3.2' else [('wordpress:plugin:translatepress-multilingual',value)])
 add('hidden',body='<html>PHP/8.5.1 PHPSESSID</html>')
 add('malformed',php='8.5')
 add('conflicting',headers=['PHP/8.5.1','PHP/8.4.1'])
 add('forbidden',php='8.5.1',status=403)
 add('server-error',php='8.5.1',status=503)
 add('blocked',php='8.5.1',body='<html><title>Access denied</title></html>')
 add('distro',php='8.3.6-0ubuntu0.24.04.1')
 add('mixed',php='8.5.11',core='6.9.4',plugin='3.3.1',want=[('php:php-src','8.5.11'),('wordpress:core','6.9.4'),('wordpress:plugin:translatepress-multilingual','3.3.1')])
 servers=[];expected=set();expected_php=set();expected_php_urls=set()
 def handler(case):
  class Site(BaseHTTPRequestHandler):
   def log_message(self,*args):pass
   def do_GET(self):
    if self.path=='/':
     self.send_response(case['status']);self.send_header('Content-Type','text/html')
     for value in case['headers'] or (["PHP/"+case['php']] if case['php'] else []):self.send_header('X-Powered-By',value)
     self.end_headers()
     body=case['body'] if case['body'] is not None else '<html><title>Dummy site</title>'+('<script src="/wp-includes/test.js"></script>' if case['core'] or case['plugin'] else '')+(f'<meta name="generator" content="WordPress {case["core"]}">' if case['core'] else '<meta name="generator" content="WordPress">' if case['plugin'] else '')+'</html>'
     self.wfile.write(body.encode())
    elif self.path=='/wp-content/plugins/translatepress-multilingual/readme.txt' and case['plugin']:
     self.send_response(200);self.send_header('Content-Type','text/plain');self.end_headers();self.wfile.write(('=== TranslatePress - Translate Multilingual sites with AI Translation ===\nStable tag: '+case['plugin']+'\n').encode())
    else:self.send_response(404);self.end_headers()
  return Site
 try:
  for case in cases:
   server=ThreadingHTTPServer(('127.0.0.1',0),handler(case));servers.append(server);threading.Thread(target=server.serve_forever,daemon=True).start()
   case['url']='http://127.0.0.1:'+str(server.server_port)+'/'
   for component,value in case['want']:
    expected.add((case['url'],component,value))
    if component=='php:php-src':expected_php.add(case['url']+'\t'+value);expected_php_urls.add(case['url'])
  runs=[]
  for n in [1,2]:
   with tempfile.TemporaryDirectory(prefix='cnc-runtime-fixtures-') as directory:
    directory=Path(directory);job='runtime-pass-'+str(n)
    rules=[dict(id=c['id'],name='Default check '+str(i+1),component=c['target']+(':'+c['identifier'] if c['target']=='wordpress:plugin' else ''),clauses=c['clauses']) for i,c in enumerate(checks)]
    artifact=dict(schema_version=1,job_id=job,target_scope=dict(hosts=[],cidrs=['127.0.0.0/8'],schemes=['http'],ports=[s.server_port for s in servers]),rules=rules)
    data=json.dumps(artifact,separators=(',',':')).encode();path=directory/'artifact.json';path.write_bytes(data)
    inputs=directory/'inputs.txt';inputs.write_text(''.join(c['url']+'\n'+c['url']+'\n' for c in cases))
    output=directory/'output'
    command=[str(args.binary.resolve()),'-l',str(inputs),'-t','16','-timeout','3s','-version-rules-file',str(path),'-version-rules-sha256',hashlib.sha256(data).hexdigest(),'-enum-job-id',job,'-enum-shard-id','fixture','-enum-output-dir',str(output)]
    result=subprocess.run(command,text=True,capture_output=True,timeout=90)
    if result.returncode:raise AssertionError('Scanner failed: '+result.stderr[-1500:])
    records=[json.loads(line) for p in output.glob('*.jsonl') for line in p.read_text().splitlines() if line]
    observed=set()
    for r in records:
     t=r['target'];url=f'{t["scheme"]}://{t["host"]}:{t["port"]}{t["path"]}'
     obs=r['observation'];observed.add((url,obs['component'],obs['version']))
     if obs['component']=='php:php-src':assert obs['confidence']=='reported'
    assert observed==expected,dict(missing=sorted(expected-observed),unexpected=sorted(observed-expected))
    assert len(records)==len(expected),'duplicate receipts'
    rows=lambda name:{line.split('] ',1)[1] for line in result.stdout.splitlines() if line.startswith('[FILE:'+name+'] ')}
    assert rows('php_versions.txt')==expected_php,'version export mismatch'
    assert rows('php-ver.txt')==expected_php_urls,'URL export mismatch'
    runs.append(dict(pass_number=n,fixture_sites=len(cases),input_lines=2*len(cases),matched_records=len(records),php_version_rows=len(expected_php),negative_controls='passed',exact_exports=True))
  report=dict(scanner_sha256=hashlib.sha256(args.binary.read_bytes()).hexdigest(),default_checks=7,loopback_only=True,runs=runs,passed=True)
  args.report.parent.mkdir(parents=True,exist_ok=True);args.report.write_text(json.dumps(report,indent=2)+'\n');print(json.dumps(report))
 finally:
  for server in servers:server.shutdown();server.server_close()
if __name__=='__main__':main()

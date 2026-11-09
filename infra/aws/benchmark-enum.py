#!/usr/bin/env python3
"""Compare two enum binaries against identical localhost workloads, never public targets."""
from collections import Counter
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time


def benchmark(before, after):
    active = peak = calls = 0
    lock = threading.Lock()
    workload = 'cms'

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_GET(self):
            nonlocal active, peak, calls
            with lock:
                active += 1
                peak = max(peak, active)
                calls += 1
            try:
                time.sleep(0.08)
                if workload == 'cms':
                    body = '<meta name="generator" content="Drupal">'
                elif self.path == '/' or self.path.startswith('/target-'):
                    body = '<meta name="generator" content="WordPress 6.4">'
                elif self.path == '/wp-json/':
                    body = '{"generator":{"name":"WordPress","version":"6.4"}}'
                    self.send_response(200)
                    self.send_header('Content-Type', 'application/json')
                    self.end_headers()
                    self.wfile.write(body.encode())
                    return
                elif self.path.endswith('/fixture/readme.txt'):
                    body = '=== Fixture ===\nStable tag: 1.2\n'
                    self.send_response(200)
                    self.send_header('Content-Type', 'text/plain')
                    self.end_headers()
                    self.wfile.write(body.encode())
                    return
                elif self.path.endswith('/fixture/package.json'):
                    body = '{"name":"fixture","version":"1.2"}'
                    self.send_response(200)
                    self.send_header('Content-Type', 'application/json')
                    self.end_headers()
                    self.wfile.write(body.encode())
                    return
                else:
                    self.send_error(404)
                    return
                self.send_response(200)
                self.send_header('Content-Type', 'text/html')
                self.end_headers()
                self.wfile.write(body.encode())
            finally:
                with lock:
                    active -= 1

    servers = [ThreadingHTTPServer(('127.0.0.1', 0), Handler) for _ in range(16)]
    for server in servers:
        threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        with tempfile.TemporaryDirectory(prefix='enum-bench-') as folder:
            root = Path(folder)
            ports = [s.server_port for s in servers]
            artifact = root / 'artifact.json'
            artifact.write_text(json.dumps({'schema_version': 1, 'job_id': 'benchmark',
                'target_scope': {'hosts': [], 'cidrs': ['127.0.0.0/8'], 'schemes': ['http'], 'ports': ports}, 'rules': []}))
            digest = hashlib.sha256(artifact.read_bytes()).hexdigest()
            wordlist = root / 'plugins.txt'
            wordlist.write_text('fixture\n')
            report = {}
            for workload, total in [('cms', 512), ('deep', 64)]:
                source = root / (workload + '.txt')
                source.write_text(''.join(f'http://127.0.0.1:{ports[i % 16]}/target-{i}\n' for i in range(total)))
                runs = {}
                outputs = []
                for label, binary in [('before', before), ('after', after)]:
                    with lock:
                        peak = calls = 0
                    env = dict(os.environ)
                    for key in ('ENUM_MAX_INFLIGHT_REQUESTS', 'ENUM_EVIDENCE_GATEWAY', 'ENUM_EVIDENCE_TOKEN'):
                        env.pop(key, None)
                    start = time.monotonic()
                    result = subprocess.run([str(binary), '-l', str(source), '-t', '256', '-timeout', '2s',
                        '--version-rules-file', str(artifact), '--version-rules-sha256', digest,
                        '--enum-job-id', 'benchmark', '--enum-shard-id', 'fixture',
                        '--enum-output-dir', str(root / (workload + '-' + label)), '-wp-plugins', str(wordlist)],
                        env=env, capture_output=True, text=True, timeout=90)
                    elapsed = time.monotonic() - start
                    assert result.returncode == 0, result.stderr
                    # The new export intentionally removes metadata and hides
                    # internal JSONL. Compare the same URL records and coverage.
                    lines = Counter(line.split('  component=',1)[0].split('  plugin=',1)[0]
                                    for line in result.stdout.splitlines() if '.txt]' in line)
                    assert len([line for line in result.stdout.splitlines() if line.startswith('[FILE:wordpress.txt]' if workload == 'deep' else '[FILE:drupal.txt]')]) == total
                    outputs.append(lines)
                    runs[label] = {'seconds': round(elapsed, 3), 'requests': calls, 'peak_active': peak,
                                   'targets': total, 'exit_code': result.returncode}
                assert outputs[0] == outputs[1], 'Result records changed'
                assert runs['before']['requests'] == runs['after']['requests'], 'Request coverage changed'
                runs['speedup'] = round(runs['before']['seconds'] / runs['after']['seconds'], 2)
                runs['same_clean_url_records'] = True
                report[workload] = runs
            return report
    finally:
        for server in servers:
            server.shutdown()
            server.server_close()


if __name__ == '__main__':
    print(json.dumps(benchmark(Path(sys.argv[1]).resolve(), Path(sys.argv[2]).resolve()), indent=2))

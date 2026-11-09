#!/usr/bin/env python3
"""Verify the actual enum executable against localhost-only public metadata fixtures."""
import hashlib
import http.server
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import threading


def verify(binary):
    results = []
    cases = [
        ('wordpress', '1.0.0', 'Fixture', True),
        ('wordpress', '2.0.0', 'Fixture', False),
        ('wordpress', '9.0.0', 'Fixture', False),
        ('wordpress', '1.0.0', 'Unrelated Plugin', False),
        ('wordpress-redirect', '1.0.0', 'Fixture', True),
        ('joomla', '1.0.0', 'PLG_SYSTEM_FIXTURE', True),
        ('joomla', '2.0.0', 'fixture', False),
        ('joomla', '9.0.0', 'fixture', False),
        ('joomla', '1.0.0', 'other', False),
    ]
    for cms, version, identity, expected in cases:
        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                if self.path == '/entry':
                    self.send_response(302)
                    self.send_header('Location', '/')
                    self.end_headers()
                    return
                body, content_type = None, 'text/plain'
                if self.path == '/':
                    brand = 'WordPress' if cms.startswith('wordpress') else 'Joomla!'
                    body = f'<html><meta name="generator" content="{brand} 6.4"></html>'
                    content_type = 'text/html'
                elif cms.startswith('wordpress') and self.path == '/wp-content/plugins/fixture/readme.txt':
                    body = f'=== {identity} ===\nStable tag: {version}\n'
                elif cms == 'joomla' and self.path == '/plugins/system/fixture/fixture.xml':
                    body = f'<extension type="plugin"><name>{identity}</name><version>{version}</version></extension>'
                    content_type = 'application/xml'
                if body is None:
                    self.send_error(404)
                    return
                self.send_response(200)
                self.send_header('Content-Type', content_type)
                self.end_headers()
                self.wfile.write(body.encode())

        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory(prefix='cnc-plugin-proof-') as directory:
                root = Path(directory)
                url = f'http://127.0.0.1:{server.server_port}/'
                component = 'wordpress:plugin:fixture' if cms.startswith('wordpress') else 'joomla:plugin:system:fixture'
                artifact = {'schema_version': 1, 'job_id': 'plugin-proof',
                            'target_scope': {'cidrs': ['127.0.0.0/8'], 'schemes': ['http'], 'ports': [server.server_port]},
                            'rules': [{'id': 'below-two', 'name': 'Below two', 'component': component,
                                       'clauses': [{'operator': '<', 'version': '2.0.0'}]}]}
                raw = json.dumps(artifact).encode()
                (root / 'artifact.json').write_bytes(raw)
                (root / 'targets.txt').write_text(url + ('entry' if cms == 'wordpress-redirect' else '') + '\n')
                output = subprocess.run([str(binary), '-l', str(root / 'targets.txt'),
                                         '--version-rules-file', str(root / 'artifact.json'),
                                         '--version-rules-sha256', hashlib.sha256(raw).hexdigest(),
                                         '--enum-job-id', 'plugin-proof', '--enum-shard-id', 'fixture-shard',
                                         '--enum-output-dir', str(root / 'results'), '-t', '4', '-timeout', '2s'],
                                        capture_output=True, text=True, timeout=30, check=True)
                marker = '[FILE:fixture.txt]' if cms.startswith('wordpress') else '[FILE:plugin:system:fixture.txt]'
                assert (marker in output.stdout) == expected, f'{cms} {version} {identity}: incorrect text match'
                if cms.startswith('wordpress'):
                    assert ('[FILE:plugins_name.txt]' in output.stdout) == expected
                assert all(len(line.split()) == 2 and line.split()[1].startswith(url.rstrip('/'))
                           for line in output.stdout.splitlines()), 'Unclean output'
                records = sum(len(f.read_text().splitlines()) for f in (root / 'results').glob('*.jsonl'))
                assert records == int(expected), 'Text and validated records disagree'
                if cms == 'wordpress-redirect':
                    record = json.loads(next((root / 'results').glob('*.jsonl')).read_text())
                    assert record['target']['path'] == '/entry', 'Redirect lost input binding'
                results.append({'cms': cms, 'version': version, 'identity': identity, 'match': expected})
        finally:
            server.shutdown()
            server.server_close()
            thread.join()
    return {'passed': len(results), 'localhost_only': True, 'clean_url_output': True, 'cases': results}


if __name__ == '__main__':
    print(json.dumps(verify(Path(sys.argv[1]).resolve())))

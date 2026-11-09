#!/usr/bin/env python3
"""Verify enum text exports against a disposable localhost WordPress fixture."""
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import threading


def verify(binary):
    plugins = ('wp-file-manager', 'litespeed-cache')
    scenario = 'wordpress'

    class Handler(BaseHTTPRequestHandler):
        def handle(self):
            try:
                super().handle()
            except (BrokenPipeError, ConnectionResetError):
                # A confirmed login cancels outstanding probes by design.
                pass

        def log_message(self, *args):
            pass

        def do_GET(self):
            if scenario == 'cpanel':
                body, mime = '<title>cPanel Login</title><form id="login_form" action="/login/"><input name="user"><input name="pass" type="password"></form>', 'text/html'
            elif scenario == 'admin':
                if self.path != '/admin':
                    self.send_error(404)
                    return
                body, mime = '<title>Acme</title><form><input name="username"><input type="password"></form>', 'text/html'
            elif self.path == '/':
                body, mime = '<meta name="generator" content="WordPress 6.4">', 'text/html'
            elif self.path == '/wp-json/':
                body, mime = '{"generator":{"name":"WordPress","version":"6.4"}}', 'application/json'
            elif any(self.path == '/wp-content/plugins/' + p + '/readme.txt' for p in plugins):
                body, mime = '=== Owned fixture ===\nStable tag: 1.2\n', 'text/plain'
            else:
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header('Content-Type', mime)
            self.end_headers()
            self.wfile.write(body.encode())

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        with tempfile.TemporaryDirectory(prefix='enum-url-fixture-') as folder:
            root = Path(folder)
            base = f'http://127.0.0.1:{server.server_port}'
            source, wordlist, artifact = (root / name for name in ('input.txt', 'plugins.txt', 'artifact.json'))
            source.write_text(base + '/\n')
            wordlist.write_text('\n'.join(plugins) + '\n')
            artifact.write_text(json.dumps({'schema_version': 1, 'job_id': 'url-fixture', 'rules': [],
                'target_scope': {'hosts': [], 'cidrs': ['127.0.0.0/8'], 'schemes': ['http'], 'ports': [server.server_port]}}))
            digest = hashlib.sha256(artifact.read_bytes()).hexdigest()
            output = root / 'output'
            result = subprocess.run([str(binary), '-l', str(source), '-t', '8', '-timeout', '2s',
                '-wp-plugins', str(wordlist), '--version-rules-file', str(artifact), '--version-rules-sha256', digest,
                '--enum-job-id', 'url-fixture', '--enum-shard-id', 'owned', '--enum-output-dir', str(output)],
                capture_output=True, text=True, timeout=30)
            if result.returncode != 0:
                raise RuntimeError('Fixture scan failed: ' + result.stderr)
            if '.jsonl' in result.stdout:
                raise RuntimeError('Internal JSONL leaked into exports')
            records = {}
            for line in result.stdout.splitlines():
                if not line.startswith('[FILE:') or '.txt]' not in line:
                    continue
                name, payload = line[6:].split(']', 1)
                payload = payload.strip()
                if len(payload.split()) != 1 or not payload.startswith(base):
                    raise RuntimeError('Text result contains metadata or nonlocal URL')
                records.setdefault(name, []).append(payload)
            for plugin in plugins:
                if records.get(plugin + '.txt') != [base + '/']:
                    raise RuntimeError('Incorrect plugin URL export')
            if sorted(records.get('plugins_name.txt', [])) != sorted(base + '/wp-content/plugins/' + p for p in plugins):
                raise RuntimeError('Incorrect plugin path URL export')
            # Materialize the same text records consumed by CNC's stream router.
            for name, lines in records.items():
                (root / name).write_text('\n'.join(lines) + '\n')
            structured = output / 'enum-versions-url-fixture-owned.jsonl'
            evidence = [json.loads(line) for line in structured.read_text().splitlines()]
            if not all(any(r['observation']['component'] == 'wordpress:plugin:' + p for r in evidence) for p in plugins):
                raise RuntimeError('Structured plugin evidence was lost')
            for scenario in ('admin', 'cpanel'):
                checked = subprocess.run([str(binary), '-l', str(source), '-t', '8', '-timeout', '2s',
                    '--version-rules-file', str(artifact), '--version-rules-sha256', digest,
                    '--enum-job-id', 'url-fixture', '--enum-shard-id', scenario,
                    '--enum-output-dir', str(root / scenario)], capture_output=True, text=True, timeout=30)
                if checked.returncode != 0 or '.jsonl' in checked.stdout:
                    raise RuntimeError('Admin fixture failed or exported JSONL')
                expected = '[FILE:admin.txt]' if scenario == 'admin' else '[FILE:cpanel.txt]'
                forbidden = '[FILE:cpanel.txt]' if scenario == 'admin' else '[FILE:admin.txt]'
                if checked.stdout.count(expected) != 1 or forbidden in checked.stdout:
                    raise RuntimeError('cPanel and ordinary login outputs were mixed')
            return {'text_files_clean': True, 'plugin_url_files': len(plugins), 'plugin_path_urls': 2,
                    'structured_evidence_preserved': True, 'jsonl_not_exported': True, 'ordinary_login_verified': True,
                    'cpanel_separated': True, 'sha256': hashlib.sha256(Path(binary).read_bytes()).hexdigest()}
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


if __name__ == '__main__':
    print(json.dumps(verify(Path(sys.argv[1]).resolve())))

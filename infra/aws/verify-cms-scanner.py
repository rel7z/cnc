#!/usr/bin/env python3
"""Exercise a CMS binary using disposable localhost fixtures only."""
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import threading

NAMES = ('wordpress', 'joomla', 'drupal', 'shopify', 'ghost', 'magento', 'wix', 'squarespace')


def verify(binary):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_GET(self):
            name = self.path.strip('/')
            self.send_response({'blocked': 403, 'error': 500}.get(name, 200))
            self.end_headers()
            body = f'<meta name="generator" content="{name}">' if name in NAMES else 'Plain page'
            self.wfile.write(body.encode())

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        with tempfile.TemporaryDirectory(prefix='cms-fixture-') as folder:
            directory = Path(folder)
            base = f'http://127.0.0.1:{server.server_port}'
            source = directory / 'input.txt'
            source.write_text(''.join(f'{base}/{name}\n' for name in (*NAMES, 'unknown', 'blocked', 'error')) + f'{base}/wordpress\nhttp://%zz\n')
            output = directory / 'results'
            result = subprocess.run([str(binary), '-l', str(source), '-o', str(output), '-t', '8', '-timeout', '2s'], capture_output=True, text=True, timeout=30)
            assert result.returncode == 2, 'Expected partial-result exit code'
            counts = {}
            for name in (*NAMES, 'cms-unknown', 'cms-blocked', 'cms-errors'):
                lines = (output / (name + '.txt')).read_text().splitlines()
                assert len(lines) == (2 if name == 'cms-errors' else 1), f'Incorrect category: {name}'
                counts[name] = len(lines)
                for line in lines:
                    assert f'[FILE:{name}.txt] {line}\n' in result.stdout, 'Missing worker output record'
            assert 'processed=12 detected=8 unknown=1 blocked=1 errors=2' in result.stderr
            before = source.read_bytes()
            # Point an output at its own input: validation must preserve it.
            alias = output / 'wordpress.txt'
            alias.write_bytes(before)
            collision = subprocess.run([str(binary), '-l', str(alias), '-o', str(output)], capture_output=True, text=True, timeout=10)
            assert collision.returncode == 1 and alias.read_bytes() == before, 'Input overwritten'
            return {'fixtures_passed': True, 'counts': counts, 'partial_exit': result.returncode,
                    'input_collision_preserved': True, 'sha256': hashlib.sha256(Path(binary).read_bytes()).hexdigest()}
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


if __name__ == '__main__':
    print(json.dumps(verify(Path(sys.argv[1]).resolve())))

import argparse
import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from api import handler
from main import connect, ingest


class Connection:
    def __init__(self, request):
        self.input = io.BytesIO(request)
        self.output = io.BytesIO()

    def makefile(self, *args):
        return self.input

    def sendall(self, data):
        self.output.write(data)


class APITests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.database = Path(self.temp.name) / 'catalog.db'
        source = Path(self.temp.name) / 'input.jsonl'
        source.write_text('\n'.join(map(json.dumps, [
            {'url': 'example.org', 'html': '<meta name="generator" content="WordPress">'},
            {'url': 'other.org'},
        ])))
        db = connect(self.database)
        try:
            with contextlib.redirect_stdout(io.StringIO()):
                ingest(db, argparse.Namespace(input=source, format='jsonl', source='api-test'))
            from intel import init_intel_tables, index_page
            init_intel_tables(db)
            index_page(db, "https://example.org/", "<html><head><title>Example</title></head><body><script>gtag('config', 'UA-1234');</script></body></html>")
        finally:
            db.close()

    def request(self, path, key='test-key', method='GET', body=None, raw=False):
        payload = b''
        headers_str = f'{method} {path} HTTP/1.0\r\nAuthorization: Bearer {key}\r\n'
        if body is not None:
            payload = json.dumps(body).encode()
            headers_str += f'Content-Type: application/json\r\nContent-Length: {len(payload)}\r\n'
        headers_str += '\r\n'
        request = headers_str.encode() + payload
        connection = Connection(request)
        handler(self.database, 'test-key')(connection, ('127.0.0.1', 1234), None)
        header_bytes, resp_body = connection.output.getvalue().split(b'\r\n\r\n', 1)
        headers_text = header_bytes.decode('utf-8', errors='replace')
        status = int(headers_text.split()[1])
        if raw or 'text/plain' in headers_text:
            return status, resp_body.decode('utf-8', errors='replace'), headers_text
        return status, json.loads(resp_body)

    def test_authentication(self):
        self.assertEqual(self.request('/api/v1/stats', 'wrong')[0], 401)

    def test_stats_and_filtered_sites(self):
        self.assertEqual(self.request('/api/v1/stats'), (200, {'urls': 2, 'imports': 1, 'wordpress': 1}))
        status, result = self.request('/api/v1/sites?wordpress=1')
        self.assertEqual(status, 200)
        self.assertEqual(result['items'], [{'url': 'https://example.org/', 'host': 'example.org'}])
        self.assertFalse(result['has_more'])

    def test_observation_provenance(self):
        status, result = self.request('/api/v1/observations?url=example.org')
        self.assertEqual(status, 200)
        self.assertEqual(result['items'][0]['source'], 'api-test')
        self.assertEqual(result['items'][0]['evidence'], ['WordPress generator tag'])

    def test_invalid_page(self):
        self.assertEqual(self.request('/api/v1/sites?page=0')[0], 400)

    def test_missing_database(self):
        self.database = Path(self.temp.name) / 'missing.db'
        self.assertEqual(self.request('/api/v1/stats')[0], 503)

    def test_intel_endpoints(self):
        # Stats
        status, stats = self.request('/api/v1/intel/stats')
        self.assertEqual(status, 200)
        self.assertEqual(stats['total_pages'], 1)

        # Search
        status, search = self.request('/api/v1/intel/search?q=gtag')
        self.assertEqual(status, 200)
        self.assertEqual(search['total'], 1)
        self.assertEqual(search['items'][0]['url'], 'https://example.org/')
        self.assertIn('Google Analytics', search['items'][0]['signatures'])
        self.assertTrue(len(search['items'][0]['snippets']) > 0)

        # Source
        status, source = self.request('/api/v1/intel/source?url=example.org')
        self.assertEqual(status, 200)
        self.assertEqual(source['title'], 'Example')
        self.assertIn("gtag('config'", source['html'])

        # Signatures summary
        status, sigs = self.request('/api/v1/intel/signatures')
        self.assertEqual(status, 200)
        self.assertTrue(any(s['name'] == 'Google Analytics' and s['count'] >= 1 for s in sigs['signatures']))

        # Recent scanner feed
        status, recent = self.request('/api/v1/scanner/recent')
        self.assertEqual(status, 200)
        self.assertEqual(len(recent['recent']), 1)
        self.assertEqual(recent['recent'][0]['url'], 'https://example.org/')

        # Export
        status, exp = self.request('/api/v1/export?wordpress=1')
        self.assertEqual(status, 200)
        self.assertEqual(exp['total'], 1)
        self.assertEqual(exp['urls'], ['https://example.org/'])

        # Scanner resources telemetry & update
        status, res = self.request('/api/v1/scanner/resources')
        self.assertEqual(status, 200)
        self.assertIn('cgroups', res)
        self.assertIn('telemetry', res)
        self.assertIn('config', res)

        status, updated = self.request('/api/v1/scanner/resources', method='POST', body={'profile': 'eco', 'concurrency': 150})
        self.assertEqual(status, 200)
        self.assertTrue(updated.get('success'))
        self.assertEqual(updated['config']['concurrency'], 150)

    def test_download_txt_endpoints(self):
        # 1. Download verified WordPress as .txt
        status, body, headers = self.request('/api/v1/export?format=txt&wordpress=1')
        self.assertEqual(status, 200)
        self.assertIn('Content-Type: text/plain; charset=utf-8', headers)
        self.assertIn('Content-Disposition: attachment; filename="wordpress-sites-', headers)
        self.assertEqual(body.strip(), 'https://example.org/')

        # 2. Download all domains as hostnames (field=host)
        status, body, headers = self.request('/api/v1/download?field=host')
        self.assertEqual(status, 200)
        self.assertIn('Content-Type: text/plain; charset=utf-8', headers)
        lines = [line.strip() for line in body.splitlines() if line.strip()]
        self.assertIn('example.org', lines)
        self.assertIn('other.org', lines)

        # 3. Download by intelligence signature
        status, body, headers = self.request('/api/v1/export.txt?signature=Google+Analytics')
        self.assertEqual(status, 200)
        self.assertIn('Content-Type: text/plain; charset=utf-8', headers)
        self.assertIn('filename="intel-google_analytics-', headers)
        self.assertEqual(body.strip(), 'https://example.org/')

        # 4. Custom filename
        status, body, headers = self.request('/api/v1/download?filename=my-custom-list.txt')
        self.assertEqual(status, 200)
        self.assertIn('Content-Disposition: attachment; filename="my-custom-list.txt"', headers)

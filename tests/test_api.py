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
        finally:
            db.close()

    def request(self, path, key='test-key'):
        request = f'GET {path} HTTP/1.0\r\nAuthorization: Bearer {key}\r\n\r\n'.encode()
        connection = Connection(request)
        handler(self.database, 'test-key')(connection, ('127.0.0.1', 1234), None)
        headers, body = connection.output.getvalue().split(b'\r\n\r\n', 1)
        return int(headers.split()[1]), json.loads(body)

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

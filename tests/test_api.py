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

    def test_database_management_and_sources(self):
        # 1. Test listing each source in the database
        status, sources_res = self.request('/api/v1/database/sources')
        self.assertEqual(status, 200)
        self.assertTrue(sources_res['total_sources'] >= 1)
        first_src = sources_res['sources'][0]
        self.assertIn('id', first_src)
        self.assertIn('source', first_src)
        self.assertIn('imported_at', first_src)
        self.assertIn('observations_count', first_src)
        self.assertIn('wordpress_count', first_src)
        self.assertEqual(first_src['source'], 'api-test')
        self.assertEqual(first_src['observations_count'], 2)

        # 2. Test sampling records from a specific source
        source_id = first_src['id']
        status, sample_res = self.request(f'/api/v1/database/sources/sample?id={source_id}')
        self.assertEqual(status, 200)
        self.assertEqual(sample_res['source']['id'], source_id)
        self.assertTrue(len(sample_res['sample']) > 0)
        self.assertEqual(sample_res['sample'][0]['url'], 'https://example.org/')

        # 3. Test downloading .txt specific to this source
        status, body, headers = self.request(f'/api/v1/export?source_id={source_id}&format=txt')
        self.assertEqual(status, 200)
        self.assertIn('Content-Type: text/plain; charset=utf-8', headers)
        self.assertIn(f'filename="source-{source_id}-', headers)
        lines = [line.strip() for line in body.splitlines() if line.strip()]
        self.assertIn('https://example.org/', lines)

        # 4. Test database stats & PRAGMA storage engine details
        status, stats_res = self.request('/api/v1/database/stats')
        self.assertEqual(status, 200)
        self.assertIn('size_bytes', stats_res)
        self.assertIn('tables', stats_res)
        self.assertIn('engine', stats_res)
        self.assertEqual(stats_res['tables']['sites'], 2)
        self.assertEqual(stats_res['tables']['imports'], 1)

        # 5. Test database maintenance operations
        for action in ('checkpoint', 'optimize', 'integrity'):
            status, maint_res = self.request('/api/v1/database/maintenance', method='POST', body={'action': action})
            self.assertEqual(status, 200)
            self.assertTrue(maint_res['success'])

        # 6. Test direct database import under a new source label
        status, import_res = self.request('/api/v1/database/import', method='POST', body={
            'source': 'test-manual-feed',
            'urls': ['https://newsite1.org/', 'https://newsite2.org/']
        })
        self.assertEqual(status, 200)
        self.assertTrue(import_res['success'])
        self.assertEqual(import_res['accepted'], 2)
        new_import_id = import_res['import_id']

        # Verify new source appears in sources list
        status, updated_sources = self.request('/api/v1/database/sources')
        self.assertEqual(status, 200)
        self.assertTrue(any(s['id'] == new_import_id and s['source'] == 'test-manual-feed' for s in updated_sources['sources']))

        # 7. Test deleting a source
        status, del_res = self.request('/api/v1/database/sources/delete', method='POST', body={'import_id': new_import_id})
        self.assertEqual(status, 200)
        self.assertTrue(del_res['success'])
        self.assertEqual(del_res['deleted_imports'], 1)

    def test_database_bundle_and_sync(self):
        # 1. Status without key or previous backup
        status, res = self.request('/api/v1/database/bundle/status')
        self.assertEqual(status, 200)
        self.assertEqual(res['status'], 'ok')
        self.assertFalse(res['is_backup_running'])

        # 2. Trigger sync
        status, sync_res = self.request('/api/v1/database/sync', method='POST')
        self.assertEqual(status, 200)
        self.assertTrue(sync_res['success'])
        self.assertEqual(sync_res['imported'], 0)

        # 3. Status reflects sync
        status, res = self.request('/api/v1/database/bundle/status')
        self.assertEqual(status, 200)
        self.assertIsNotNone(res['sync'])
        self.assertEqual(res['sync']['data']['imported'], 0)

        # 4. Attempt bundle creation without key should return error
        status, err_bundle = self.request('/api/v1/database/bundle/create', method='POST')
        self.assertEqual(status, 500)
        self.assertFalse(err_bundle['success'])
        self.assertIn('Recovery key missing', err_bundle['message'])

        # 5. Provide key and test bundle creation and verification
        import os
        from bundle import DATABASES
        key_file = Path(self.temp.name) / 'internet-map-recovery.key'
        key_file.write_bytes(b'k' * 32)
        old_key_env = os.environ.get('RECOVERY_KEY_PATH')
        os.environ['RECOVERY_KEY_PATH'] = str(key_file)
        try:
            # Create dummy secondary databases and minimum source files so bundle passes
            (Path(self.temp.name) / 'api.py').write_text('# api')
            (Path(self.temp.name) / 'infra').mkdir(parents=True, exist_ok=True)
            (Path(self.temp.name) / 'infra/install-services.py').write_text('# install')
            for rel in DATABASES:
                p = Path(self.temp.name) / rel
                p.parent.mkdir(parents=True, exist_ok=True)
                if not p.exists():
                    with connect(p) as db_dummy:
                        db_dummy.execute('CREATE TABLE dummy(x)')

            status, ok_bundle = self.request('/api/v1/database/bundle/create', method='POST')
            self.assertEqual(status, 200)
            self.assertTrue(ok_bundle['success'])

            # Wait briefly or simulate bundle completion
            import time
            time.sleep(0.5)

            status, res = self.request('/api/v1/database/bundle/status')
            self.assertEqual(status, 200)
            self.assertTrue(res['key_configured'])
            self.assertIn('drive', res)

            # Test verify endpoint
            status, verify_res = self.request('/api/v1/database/bundle/verify', method='POST')
            self.assertEqual(status, 200)
            self.assertTrue(verify_res['success'])
            self.assertEqual(verify_res['status'], 'running')
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                _, current = self.request('/api/v1/database/bundle/status')
                verification = current.get('verification') or {}
                if verification.get('status') != 'running':
                    break
                time.sleep(0.01)
            self.assertEqual(verification['integrity'], 'ok')
            self.assertEqual(verification['scope'], 'local-archive')
        finally:
            if old_key_env is not None:
                os.environ['RECOVERY_KEY_PATH'] = old_key_env
            else:
                os.environ.pop('RECOVERY_KEY_PATH', None)

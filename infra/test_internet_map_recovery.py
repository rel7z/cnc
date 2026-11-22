"""Recovery exercises authenticated cloud bytes against disposable databases."""
import hashlib
import hmac
import io
import json
from pathlib import Path
import sqlite3
import tarfile
import tempfile
import unittest
from unittest.mock import patch

from infra import internet_map_recovery as recovery


class RecoveryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.key = b'owner-private-fixture-key-value!!'

    def archive(self, extra=None):
        entries, content = {}, {}
        for index, relative in enumerate(recovery.DATABASES):
            database = self.root / str(index)
            with sqlite3.connect(database) as db:
                db.execute('CREATE TABLE proof(value)')
                db.execute('INSERT INTO proof VALUES (?)', (relative,))
            db.close()
            content[relative] = database.read_bytes()
        content.update(extra or {})
        for name, data in content.items():
            entries[name] = dict(bytes=len(data), sha256=hashlib.sha256(data).hexdigest())
        manifest = dict(format='internet-map-bundle-v2', source_included=False, entries=entries)
        manifest['signature'] = hmac.new(self.key, recovery.canonical(manifest), 'sha256').hexdigest()
        output = io.BytesIO()
        with tarfile.open(fileobj=output, mode='w:gz') as tar:
            for name, data in [('manifest.json', recovery.canonical(manifest)), *content.items()]:
                info = tarfile.TarInfo(name)
                info.size = len(data)
                tar.addfile(info, io.BytesIO(data))
        return output.getvalue()

    def test_downloads_latest_bundle_and_restores_verified_paused_data(self):
        data = self.archive()
        name = 'internet-map-backup-1795373914-' + 'a'*32 + '.tar.gz'
        self.cloud(data, name)
        destination = self.root / 'restored'
        result = recovery.recover(self.root/'config.json', destination, self.root/'key')
        self.assertEqual(result['status'], 'restored')
        self.assertEqual(result['sha256'], hashlib.sha256(data).hexdigest())
        self.assertTrue((destination/'data/discovery/paused').is_file())
        for relative in recovery.DATABASES:
            with sqlite3.connect(destination/relative) as db:
                self.assertEqual(db.execute('SELECT value FROM proof').fetchone(), (relative,))
            db.close()
        self.assertTrue(any('?alt=media' in url for url in self.requests))
        self.assertFalse(any(p.name.startswith('.map-download-') for p in self.root.iterdir()))

    def cloud(self, data, name):
        (self.root/'key').write_bytes(self.key)
        (self.root/'token.json').write_text(json.dumps(dict(refresh_token='fixture')))
        (self.root/'config.json').write_text(json.dumps(dict(gdrive=dict(enabled=True,
            token_path=str(self.root/'token.json'), client_id='fixture', client_secret='fixture'))))
        self.requests = []
        def open_request(request, timeout):
            url = request.full_url
            self.requests.append(url)
            if 'oauth2.googleapis.com' in url:
                value = json.dumps(dict(access_token='fixture')).encode()
            elif '?alt=media' in url:
                value = data
            else:
                value = json.dumps(dict(files=[dict(id='fixture', name=name, size=str(len(data)))])).encode()
            return io.BytesIO(value)
        client = unittest.mock.Mock()
        client.open.side_effect = open_request
        mock = patch.object(recovery.urllib.request, 'build_opener', return_value=client)
        mock.start()
        self.addCleanup(mock.stop)

    def test_wrong_key_preserves_missing_destination(self):
        data = self.archive()
        self.cloud(data, 'internet-map-backup-1795373914-' + 'a'*32 + '.tar.gz')
        (self.root/'key').write_bytes(b'x'*32)
        with self.assertRaisesRegex(ValueError, 'signature'):
            recovery.recover(self.root/'config.json', self.root/'restored', self.root/'key')
        self.assertFalse((self.root/'restored').exists())

    def test_existing_application_is_never_overwritten_or_contacted(self):
        destination = self.root/'existing'
        destination.mkdir()
        (destination/'keep').write_text('unchanged')
        with patch.object(recovery.urllib.request, 'build_opener') as network:
            self.assertEqual(recovery.recover('missing', destination, 'missing')['status'], 'existing')
        network.assert_not_called()
        self.assertEqual((destination/'keep').read_text(), 'unchanged')

    def test_authenticated_path_traversal_is_rejected(self):
        path = self.root/'unsafe.tar.gz'
        path.write_bytes(self.archive({'../outside': b'bad'}))
        with self.assertRaisesRegex(ValueError, 'Unsafe'):
            recovery.restore_bundle(path, self.root/'restored', self.key)
        self.assertFalse((self.root/'outside').exists())
        self.assertFalse((self.root/'restored').exists())


if __name__ == '__main__':
    unittest.main()

import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from coordination import DiscoveryControl, CNCClient
from backup import snapshot, publish_snapshot, restore_snapshot

class CoordinationTests(unittest.TestCase):
    def test_unconfigured_cannot_resume_and_pause_does_not_delete_pool(self):
        with tempfile.TemporaryDirectory() as tmp:
            control=DiscoveryControl(Path(tmp)/'catalog.db')
            self.assertFalse(control.status()['scanning_enabled'])
            with self.assertRaises(ValueError):control.control('resume')
            control.root.mkdir(exist_ok=True)
            (control.root/'candidates.txt').write_text('keep')
            self.assertFalse(control.control('pause')['scanning_enabled'])
            self.assertEqual((control.root/'candidates.txt').read_text(),'keep')

    def test_verified_cutover_can_resume_and_pause(self):
        with tempfile.TemporaryDirectory() as tmp:
            control=DiscoveryControl(Path(tmp)/'catalog.db',cnc=object())
            control.root.mkdir();(control.root/'cutover.json').write_text('{}')
            self.assertTrue(control.control('resume')['scanning_enabled'])
            self.assertFalse(control.control('pause')['scanning_enabled'])

    def test_snapshot_includes_wal_commits_not_uncommitted_writer(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'live.db';out=Path(tmp)/'snapshot.db'
            with sqlite3.connect(path) as writer:
                writer.execute('PRAGMA journal_mode=WAL');writer.execute('CREATE TABLE records(value)')
                writer.execute("INSERT INTO records VALUES ('committed')");writer.commit()
                writer.execute("INSERT INTO records VALUES ('pending')")
                receipt=snapshot(path,out)
                with sqlite3.connect(out) as reader:self.assertEqual(reader.execute('SELECT value FROM records').fetchall(),[('committed',)])
                self.assertEqual(receipt['integrity'],'ok')
                writer.rollback()

    def test_connection_rejects_embedded_credentials_and_paths(self):
        for origin in ['https://user:password@host','https://host/path','https://host?secret=bad']:
            with self.assertRaises(ValueError):CNCClient(origin,'x'*32)

    def test_drive_parts_roundtrip_and_reject_tampering(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);source=root/'live.db'
            with sqlite3.connect(source) as db:
                db.execute('CREATE TABLE content(value)');db.execute("INSERT INTO content VALUES ('verified')")
            receipt=publish_snapshot(source,root/'drive')
            manifest=root/'drive'/receipt['manifest']
            self.assertTrue(manifest.name.endswith('.json.gz'))
            self.assertIn(b'\x00',manifest.read_bytes()[:10])
            restore_snapshot(manifest,root/'restored.db')
            with sqlite3.connect(root/'restored.db') as db:self.assertEqual(db.execute('SELECT value FROM content').fetchone(),('verified',))
            part=root/'drive'/receipt['parts'][0]['filename'];part.write_bytes(b'corrupt')
            with self.assertRaises(ValueError):restore_snapshot(manifest,root/'broken.db')
            self.assertFalse((root/'broken.db').exists())

    def test_generator_classifications_are_idempotent_and_keep_provenance(self):
        from catalog_sync import sync_verified
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);state=root/'state.db';catalog=root/'catalog.db'
            with sqlite3.connect(state) as db:
                db.execute('CREATE TABLE merged_hosts(host TEXT PRIMARY KEY,url TEXT)')
                db.execute("INSERT INTO merged_hosts VALUES ('wp.example','https://wp.example/')")
            self.assertEqual(sync_verified(catalog,state),{'imported':1})
            self.assertEqual(sync_verified(catalog,state),{'imported':0})
            with sqlite3.connect(catalog) as db:
                self.assertEqual(db.execute('SELECT count(*) FROM sites').fetchone(),(1,))
                evidence=db.execute('SELECT evidence FROM observations').fetchone()[0]
                self.assertIn('generator classification',evidence)
                self.assertNotIn('generator tag',evidence)

    def test_failed_cutover_restores_original_state_path(self):
        import importlib.util
        from unittest.mock import patch, Mock
        spec=importlib.util.spec_from_file_location('map_migration',Path(__file__).parents[1]/'infra/install-discovery.py')
        migration=importlib.util.module_from_spec(spec);spec.loader.exec_module(migration)
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);old=root/'old';state=root/'new';units=root/'units'
            old.mkdir();units.mkdir();(old/'checkpoint').write_text('preserve')
            def fail():
                old.rename(state);old.symlink_to(state,target_is_directory=True)
                raise RuntimeError('activation failed')
            with patch.multiple(migration,OLD=old,STATE=state,UNITS=units),patch.object(migration,'perform_install',side_effect=fail),patch.object(migration.subprocess,'run',return_value=Mock(returncode=0)):
                with self.assertRaises(RuntimeError):migration.install()
            self.assertTrue(old.is_dir());self.assertFalse(old.is_symlink())
            self.assertEqual((old/'checkpoint').read_text(),'preserve')
            self.assertFalse(state.exists())

    def test_migrated_legacy_lease_api_does_not_assign_fresh_work(self):
        import io
        from api import handler
        class Connection:
            def __init__(self,raw):self.input=io.BytesIO(raw);self.output=io.BytesIO()
            def makefile(self,*args):return self.input
            def sendall(self,data):self.output.write(data)
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);(root/'discovery').mkdir();(root/'discovery/cutover.json').write_text('{}')
            raw=b'POST /api/v1/cluster/lease HTTP/1.0\r\nAuthorization: Bearer test-key\r\nContent-Length: 2\r\n\r\n{}'
            connection=Connection(raw);handler(root/'catalog.db','test-key')(connection,('127.0.0.1',1234),None)
            header,body=connection.output.getvalue().split(b'\r\n\r\n',1)
            self.assertIn(b'200',header.splitlines()[0]);reply=json.loads(body)
            self.assertEqual(reply['urls'],[]);self.assertTrue(reply['legacy_draining'])

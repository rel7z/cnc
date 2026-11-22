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

from contextlib import closing
from pathlib import Path
import sqlite3
import tarfile
import tempfile
import unittest
from bundle import DATABASES, create_bundle, restore_bundle

class BundleTests(unittest.TestCase):
    def make_source(self, root):
        for relative in DATABASES:
            p=root/relative;p.parent.mkdir(parents=True,exist_ok=True)
            with closing(sqlite3.connect(p)) as db,db:
                db.execute('CREATE TABLE proof(value)');db.execute('INSERT INTO proof VALUES (?)',(relative,))
        (root/'service.env').write_text('SECRET=excluded')
        (root/'data/discovery/generator.env').write_text('SECRET=excluded')

    def test_one_compressed_file_restores_all_databases_and_excludes_secrets(self):
        with tempfile.TemporaryDirectory() as tmp:
            r=Path(tmp);source=r/'source';self.make_source(source)
            receipt=create_bundle(source,r/'published',b'k'*32,include_source=False)
            self.assertEqual(len(list((r/'published').iterdir())),1)
            archive=r/'published'/receipt['filename']
            with tarfile.open(archive) as t:
                names=t.getnames();self.assertEqual(names[0],'manifest.json')
                self.assertFalse(any(n.endswith('.env') for n in names))
            result=restore_bundle(archive,r/'restored',b'k'*32)
            self.assertEqual(result['integrity'],'ok')
            for relative in DATABASES:
                with closing(sqlite3.connect(r/'restored'/relative)) as db:
                    self.assertEqual(db.execute('SELECT value FROM proof').fetchone(),(relative,))
            self.assertTrue((r/'restored/data/discovery/paused').exists())

    def test_wrong_key_and_existing_destination_are_rejected_without_overwrite(self):
        with tempfile.TemporaryDirectory() as tmp:
            r=Path(tmp);source=r/'source';self.make_source(source)
            receipt=create_bundle(source,r/'published',b'k'*32,include_source=False)
            archive=r/'published'/receipt['filename']
            with self.assertRaises(ValueError):restore_bundle(archive,r/'wrong-key',b'x'*32)
            self.assertFalse((r/'wrong-key').exists())
            existing=r/'existing';existing.mkdir();(existing/'keep').write_text('keep')
            with self.assertRaises(ValueError):restore_bundle(archive,existing,b'k'*32)
            self.assertEqual((existing/'keep').read_text(),'keep')

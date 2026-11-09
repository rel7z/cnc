"""Exercise the remote installer's directory setup without running deployment."""
import ast
from pathlib import Path
import tempfile
import unittest


def installer_function():
    tree = ast.parse(Path(__file__).with_name('migrate-existing-controller-remote.py').read_text())
    function = next(n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == 'ensure_merged_directory')
    namespace = {}
    exec(compile(ast.Module(body=[function], type_ignores=[]), '<merged-directory>', 'exec'), namespace)
    return namespace['ensure_merged_directory']


class MergedDirectoryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.app = Path(self.temp.name) / 'app'
        self.home = Path(self.temp.name) / 'home'
        self.app.mkdir(); self.home.mkdir()
        self.ensure = installer_function()

    def test_alias_shares_files_and_survives_redeployment(self):
        watch = self.ensure(self.app, self.home)
        (watch / 'restored.txt').write_text('fixture')
        self.assertEqual((self.home / 'merged/restored.txt').read_text(), 'fixture')
        (self.home / 'merged/new.txt').write_text('new')
        self.assertEqual((watch / 'new.txt').read_text(), 'new')
        self.assertEqual(self.ensure(self.app, self.home), watch)
        self.assertTrue((self.home / 'merged').samefile(watch))

    def test_existing_folder_is_never_overwritten(self):
        existing = self.home / 'merged'
        existing.mkdir()
        (existing / 'keep.txt').write_text('keep')
        with self.assertRaises(RuntimeError):
            self.ensure(self.app, self.home)
        self.assertFalse(existing.is_symlink())
        self.assertEqual((existing / 'keep.txt').read_text(), 'keep')

    def test_wrong_or_dangling_alias_is_rejected(self):
        alias = self.home / 'merged'
        alias.symlink_to(self.home / 'missing')
        with self.assertRaises(RuntimeError):
            self.ensure(self.app, self.home)
        self.assertEqual(alias.readlink(), self.home / 'missing')

    def test_storage_symlink_is_rejected(self):
        (self.app / 'merged').symlink_to(self.home)
        with self.assertRaises(RuntimeError):
            self.ensure(self.app, self.home)


if __name__ == '__main__':
    unittest.main()

import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('audit_setup', Path(__file__).with_name('configure-dashboard-audit.py'))
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


class DashboardAuditSetupTests(unittest.TestCase):
    def test_shared_key_is_private_and_stable(self):
        with tempfile.TemporaryDirectory() as directory:
            base=Path(directory);root=base/'cnc';units=base/'units'
            audit.configure(root, units)
            path=root/'dashboard-audit.env';original=path.read_bytes()
            self.assertGreaterEqual(len(original.split(b'=',1)[1].strip()),32)
            self.assertEqual(path.stat().st_mode&0o777,0o600)
            for service in ('cnc-server','cnc-ui'):
                dropin=units/(service+'.service.d')/'zz-dashboard-audit.conf'
                self.assertEqual(dropin.read_text(),'[Service]\nEnvironmentFile='+str(path)+'\n')
            audit.configure(root, units)
            self.assertEqual(original,path.read_bytes())

    def test_invalid_or_symlink_key_is_not_rotated_or_followed(self):
        with tempfile.TemporaryDirectory() as directory:
            base=Path(directory);root=base/'cnc';root.mkdir();path=root/'dashboard-audit.env'
            path.write_text('CNC_DASHBOARD_AUDIT_SECRET=short\n')
            with self.assertRaises(RuntimeError):audit.configure(root,base/'units')
            self.assertEqual(path.read_text(),'CNC_DASHBOARD_AUDIT_SECRET=short\n')
            path.unlink();outside=base/'outside';outside.write_text('unchanged');path.symlink_to(outside)
            with self.assertRaises(RuntimeError):audit.configure(root,base/'units')
            self.assertEqual(outside.read_text(),'unchanged')

if __name__=='__main__':unittest.main()

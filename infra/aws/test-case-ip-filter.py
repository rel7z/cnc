import subprocess
import sys
import unittest
from case_ip_filter import FILTER, filtered_command
from pathlib import Path
import tempfile

class CaseIPFilterTests(unittest.TestCase):
    def test_only_standalone_addresses_are_removed(self):
        source='1.2.3.4\n 2001:db8::1 \nexample.test\nhttps://example.test/\n999.1.1.1\n1.2.3.4:21\n'
        result=subprocess.run([sys.executable,'-c',FILTER],input=source,capture_output=True,text=True,check=True)
        self.assertEqual(result.stdout,'example.test\nhttps://example.test/\n999.1.1.1\n1.2.3.4:21\n')

    def test_provider_failure_is_not_hidden_by_pipeline(self):
        with tempfile.TemporaryDirectory() as directory:
            out=Path(directory)/'result.txt'
            command=filtered_command(['sh','-c',"printf '1.2.3.4\\nexample.test\\n'; exit 7"],str(out))
            result=subprocess.run(['sh','-c',command],capture_output=True,text=True)
            self.assertEqual(result.returncode,7)
            self.assertEqual(out.read_text(),'example.test\n')

if __name__=='__main__':unittest.main()

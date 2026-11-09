import hashlib,importlib.util,json,tempfile,unittest
from pathlib import Path
spec=importlib.util.spec_from_file_location('manifest',Path(__file__).resolve().parents[1]/'cnc-api/worker_tools_manifest.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class ManifestTest(unittest.TestCase):
 def fixture(self):
  temp=tempfile.TemporaryDirectory();self.addCleanup(temp.cleanup);root=Path(temp.name)
  files=[]
  for name in ('cnc-enum','dork2','dork2-src/index.js','dork2-src/stream-run.js'):
   p=root/name;p.parent.mkdir(exist_ok=True);p.write_text('LOCAL FIXTURE\n');p.chmod(0o700);files.append({'path':name,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()})
  manifest={'version':1,'required_executables':['cnc-enum','dork2'],'files':files};(root/'.cnc-manifest.json').write_text(json.dumps(manifest));return root,manifest
 def test_complete_package(self):
  root,_=self.fixture();self.assertEqual(m.verify(root),4)
 def test_missing_streaming_module_is_not_ready(self):
  root,manifest=self.fixture();manifest['files']=[f for f in manifest['files'] if f['path']!='dork2-src/stream-run.js'];(root/'.cnc-manifest.json').write_text(json.dumps(manifest))
  with self.assertRaises(RuntimeError):m.verify(root)
 def test_enum_only_package_is_not_ready(self):
  root,manifest=self.fixture();manifest['required_executables']=['cnc-enum'];(root/'.cnc-manifest.json').write_text(json.dumps(manifest))
  with self.assertRaises(RuntimeError):m.verify(root)
 def test_missing_and_corrupt_tool_are_not_ready(self):
  for missing in (False,True):
   root,_=self.fixture();p=root/'dork2'
   if missing:p.unlink()
   else:p.write_text('CORRUPT')
   with self.assertRaises(RuntimeError):m.verify(root)
 def test_manifest_cannot_escape_package(self):
  root,manifest=self.fixture();manifest['files'][0]['path']='../outside';(root/'.cnc-manifest.json').write_text(json.dumps(manifest))
  with self.assertRaises(RuntimeError):m.verify(root)
if __name__=='__main__':unittest.main()

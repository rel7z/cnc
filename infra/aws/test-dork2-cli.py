"""Run the patched generic CLI against an offline SDK fixture."""
import json,os,subprocess,tempfile,unittest
from pathlib import Path
class CLIRegression(unittest.TestCase):
 def fixture(self,failed):
  temp=tempfile.TemporaryDirectory();self.addCleanup(temp.cleanup);root=Path(temp.name)
  for name in ('index.js','cnc-provider-queue.mjs'):(root/name).write_bytes((Path('/private/tmp/cnc-dork2-fix')/name).read_bytes())
  (root/'package.json').write_text('{"type":"module"}')
  module=root/'node_modules/apify-client';module.mkdir(parents=True);(module/'package.json').write_text('{"type":"module","exports":"./index.js"}')
  (module/'index.js').write_text('''export class ApifyClient {
    actor(){return {start:async()=>({id:'fixture',defaultDatasetId:'fixture'})};}
    dataset(){return {listItems:async({offset=0}={})=>({items:offset||process.env.FIXTURE_FAILURE?[]:[{organicResults:[{url:'https://example.test/fixture'}]}]})};}
    run(){return {get:async()=>({id:'fixture',defaultDatasetId:'fixture',status:process.env.FIXTURE_FAILURE?'FAILED':'SUCCEEDED'}),abort:async()=>({})};}
  }''')
  env=dict(os.environ,APIFY_TOKEN='offline-fixture',FIXTURE_FAILURE='1' if failed else '')
  return root,subprocess.run(['node',str(root/'index.js'),'-k','offline fixture','-o','result.txt'],cwd=root,env=env,capture_output=True,text=True,timeout=15)
 def test_success_streams_domains_and_writes_file(self):
  root,r=self.fixture(False);self.assertEqual(r.returncode,0,r.stderr);self.assertEqual(r.stdout,'example.test\n');self.assertEqual((root/'result.txt').read_text(),'example.test\n')
 def test_query_failure_is_not_reported_as_success(self):
  root,r=self.fixture(True);self.assertEqual(r.returncode,1,r.stderr);self.assertIn('1 query failures',r.stderr);self.assertEqual(r.stdout,'')
if __name__=='__main__':unittest.main()

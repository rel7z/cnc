import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import ts from 'typescript';
import * as auth from '../lib/auth.ts';
const require=createRequire(import.meta.url);
function compile(path,imports={}){const code=ts.transpileModule(readFileSync(new URL(path,import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX}}).outputText;const module={exports:{}};new Function('require','module','exports',code)(name=>imports[name]??require(name),module,module.exports);return module.exports;}
const helpers=compile('../lib/processor-editor.ts');
const definition={tool_id:'owned',incoming:'wordpress.txt',folder:'examples/owned',manifest:{input:'wordpress.txt',scripts:{scanner:{path:'scanner',runtime:'binary',outputs:[{path:'results.txt',send_to:'checker',drive:false}]},checker:{path:'nested/check.js',input:'results.txt',package_lock:'package-lock.json',outputs:[{path:'success.txt',drive:true}]}}}};
test('processor templates preserve mixed runtimes, connections, input overrides, and internal outputs',()=>{
 assert.deepEqual(helpers.validateDefinition(definition),{});
 const loaded=helpers.parseProcessorTemplate(helpers.serializeProcessorTemplate('Owned chain',definition),helpers.blankDefinition());
 assert.equal(loaded.name,'Owned chain');assert.deepEqual(loaded.definition,definition);
 loaded.definition.manifest.scripts.scanner.outputs[0].drive=true;assert.equal(definition.manifest.scripts.scanner.outputs[0].drive,false);
 const raw=helpers.parseProcessorTemplate(JSON.stringify(definition.manifest),helpers.blankDefinition());assert.deepEqual(raw.definition.manifest,definition.manifest);
});
test('invalid templates, cycles and output conflicts cannot replace a valid form',()=>{
 for(const invalid of ['invalid',JSON.stringify({format:'unknown'}),' '.repeat(256*1024+1),JSON.stringify({...definition,manifest:{...definition.manifest,files:'not-an-array'}})])assert.throws(()=>helpers.parseProcessorTemplate(invalid,definition));
 const cycle=structuredClone(definition);cycle.manifest.scripts.checker.outputs[0].send_to='scanner';assert.ok(helpers.validateDefinition(cycle).connections);
 const conflict=structuredClone(definition);conflict.manifest.scripts.scanner.outputs[0].path='wordpress.txt';assert.ok(helpers.validateDefinition(conflict)['scanner.output.0']);
 const hidden=structuredClone(definition);hidden.incoming='.hidden.txt';assert.ok(helpers.validateDefinition(hidden).incoming);
});
test('processor proxy enforces authentication and same-origin writes, preserves download headers',async()=>{
 process.env.CNC_SESSION_SECRET='session-secret-'.repeat(4);process.env.CNC_PROCESSOR_TOKEN='processor-token-'.repeat(4);
 const {proxyProcessorEditor}=compile('../lib/processor-editor-proxy.ts',{'@/lib/auth':auth});
 const make=(method='GET',signed=true,origin='http://dashboard.test')=>{const request=new Request('http://dashboard.test/api/processor/editor/results?name=owned__success.txt',{method,headers:{host:'dashboard.test',origin},...(method==='GET'?{}:{body:'{}'})});request.cookies={get:()=>signed?{value:auth.createSession()}:undefined};request.nextUrl=new URL(request.url);return request;};
 assert.equal((await proxyProcessorEditor(make('GET',false),['results'])).status,401);
 assert.equal((await proxyProcessorEditor(make('POST',true,'http://foreign.test'),['templates'])).status,403);
 assert.equal((await proxyProcessorEditor(make('POST',false),['reset'])).status,401);
 assert.equal((await proxyProcessorEditor(make('POST',true,'http://foreign.test'),['reset'])).status,403);
 const original=globalThis.fetch;let called;
 globalThis.fetch=async(url,options)=>{called={url:String(url),options};return new Response('owned\n',{headers:{'content-type':'text/plain; charset=utf-8','content-disposition':'attachment; filename=owned__success.txt'}});};
 try{const result=await proxyProcessorEditor(make(),['results']);assert.equal(result.status,200);assert.match(result.headers.get('content-disposition'),/attachment/);assert.equal(await result.text(),'owned\n');assert.match(called.url,/processor-results\?name=owned__success.txt/);assert.equal(called.options.headers.Authorization,'Bearer '+process.env.CNC_PROCESSOR_TOKEN);await proxyProcessorEditor(make('POST'),['reset']);assert.match(called.url,/\/v1\/checkpoints\/reset$/);assert.equal(called.options.method,'POST');}finally{globalThis.fetch=original;}
});


test('configuration export is self-contained in the controller and preserves connections',()=>{
 const config=JSON.parse(helpers.serializeControllerConfig(definition));
 assert.deepEqual(config.routes,{[definition.incoming]:[definition.tool_id]});
 assert.equal(config.programs.owned.folder,definition.folder);
 assert.deepEqual(config.programs.owned.definition,definition.manifest);
 assert.equal(config.programs.owned.manifest,undefined);
 assert.equal(config.programs.owned.definition.scripts.scanner.outputs[0].send_to,'checker');
});

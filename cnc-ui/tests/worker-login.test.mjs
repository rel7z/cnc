import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import ts from 'typescript';
const require = createRequire(import.meta.url);
function compile(path, imports = {}) {
 const module = {exports:{}};
 const code = ts.transpileModule(readFileSync(new URL(path, import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX}}).outputText;
 new Function('require','module','exports',code)(name=>imports[name]??require(name),module,module.exports);
 return module.exports;
}
function nodes(n) { if(!n||typeof n!=='object')return [];if(Array.isArray(n))return n.flatMap(nodes);return[n,...nodes(n.props?.children)]; }
function text(n) { if(n==null||typeof n==='boolean')return '';if(typeof n!=='object')return String(n);if(Array.isArray(n))return n.map(text).join('');return text(n.props?.children); }
const button=(tree,label)=>nodes(tree).find(n=>n.type==='button'&&text(n)===label);
const flush=()=>new Promise(resolve=>setImmediate(resolve));
function harness() {
 let cursor=0;const cells=[];
 const react={useState(initial){const i=cursor++;if(!(i in cells))cells[i]=initial;return[cells[i],v=>{cells[i]=typeof v==='function'?v(cells[i]):v;}];},useRef(initial){const i=cursor++;return cells[i]??={current:initial};},useEffect(){}};
 const {WorkerSSH}=compile('../components/dashboard/WorkerSSH.tsx',{react});
 return {render(){cursor=0;return WorkerSSH({worker:{id:'worker-1',address:'192.0.2.1'}});}};
}
test('worker password reveals only on demand, copies, hides, saves and clears on close',async()=>{
 const original=globalThis.fetch, descriptor=Object.getOwnPropertyDescriptor(globalThis,'navigator');let copied,calls=[];
 Object.defineProperty(globalThis,'navigator',{configurable:true,value:{clipboard:{async writeText(value){copied=value;}}}});
 globalThis.fetch=async(path,opts)=>{calls.push({path,body:JSON.parse(opts.body)});return Response.json(calls.at(-1).body.action==='save'?{saved:true}:{password:'fixture-pass'});};
 try {
  const ui=harness();button(ui.render(),'SSH').props.onClick();let tree=ui.render();
  assert.equal(calls.length,0);assert.equal(nodes(tree).find(n=>n.props?.['aria-label']==='Worker login password'),undefined);
  await button(tree,'Show password').props.onClick();await flush();tree=ui.render();
  assert.equal(calls[0].path,'/api/workers/login');assert.deepEqual(calls[0].body,{worker_id:'worker-1',action:'reveal'});
  assert.equal(nodes(tree).find(n=>n.props?.['aria-label']==='Worker login password').props.value,'fixture-pass');
  button(tree,'Copy password').props.onClick();await flush();assert.equal(copied,'fixture-pass');
  button(ui.render(),'Hide password').props.onClick();assert.equal(button(ui.render(),'Copy password'),undefined);
  nodes(ui.render()).find(n=>n.type==='input'&&n.props.type==='password').props.onChange({target:{value:'known-fixture'}});
  button(ui.render(),'Save password').props.onClick();await flush();assert.deepEqual(calls[1].body,{worker_id:'worker-1',action:'save',password:'known-fixture'});
  tree=ui.render();nodes(tree).find(n=>n.props?.['aria-label']==='Close SSH instructions').props.onClick();button(ui.render(),'SSH').props.onClick();
  assert.equal(nodes(ui.render()).find(n=>n.type==='input'&&n.props.type==='password').props.value,'');
  let resolve;globalThis.fetch=()=>new Promise(r=>{resolve=r;});
  button(ui.render(),'Show password').props.onClick();nodes(ui.render()).find(n=>n.props?.['aria-label']==='Close SSH instructions').props.onClick();resolve(Response.json({password:'late-secret'}));await flush();button(ui.render(),'SSH').props.onClick();
  assert.equal(nodes(ui.render()).find(n=>n.props?.['aria-label']==='Worker login password'),undefined);
 } finally {globalThis.fetch=original;if(descriptor)Object.defineProperty(globalThis,'navigator',descriptor);else delete globalThis.navigator;}
});

test('worker login proxy requires session, same origin, bounded body and never caches',async()=>{
 const original=globalThis.fetch,oldSecret=process.env.CNC_SESSION_SECRET;process.env.CNC_SESSION_SECRET='fixture-session-secret-'.repeat(3);
 const auth=compile('../lib/auth.ts');const {POST}=compile('../app/api/workers/login/route.ts',{'@/lib/auth':auth});
 function request({signed=true,origin='http://dashboard.test',body='{}'}={}){const r=new Request('http://dashboard.test/api/workers/login',{method:'POST',headers:{host:'dashboard.test',origin},body});r.cookies={get:()=>signed?{value:auth.createSession()}:undefined};return r;}
 let calls=0;globalThis.fetch=async(url,options)=>{calls++;assert.match(url,/\/api\/workers\/login$/);assert.equal(options.cache,'no-store');return Response.json({password:'fixture-pass'});};
 try {
  assert.equal((await POST(request({signed:false}))).status,401);
  assert.equal((await POST(request({origin:'http://other.test'}))).status,403);
  assert.equal((await POST(request({body:'x'.repeat(4097)}))).status,413);assert.equal(calls,0);
  const response=await POST(request());assert.equal(response.status,200);assert.equal(response.headers.get('Cache-Control'),'no-store');assert.equal(calls,1);
  globalThis.fetch=async()=>{throw Error('secret error');};const failed=await POST(request());assert.equal(failed.status,503);assert.doesNotMatch(await failed.text(),/secret error/);
 }finally{globalThis.fetch=original;if(oldSecret===undefined)delete process.env.CNC_SESSION_SECRET;else process.env.CNC_SESSION_SECRET=oldSecret;}
});

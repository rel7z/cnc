import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import ts from 'typescript';
const require=createRequire(import.meta.url),Editor=()=>null,Results=()=>null;
function harness(){
 const cells=[],effects=[];let cursor=0,mounted=false;
 const react={useCallback:fn=>fn,useEffect:fn=>{if(!mounted)effects.push(fn);},useRef(initial){const i=cursor++;return cells[i]??={current:initial};},useState(initial){const i=cursor++;if(!(i in cells))cells[i]=typeof initial==='function'?initial():initial;return [cells[i],value=>{cells[i]=typeof value==='function'?value(cells[i]):value;}];}};
 const imports={react,'next/link':()=>null,'./ProcessorEditor':{ProcessorEditor:Editor,ProcessorResults:Results},'./WorkerSSH':{WorkerSSH:()=>null},'./ToolPriorities':{ToolPriorities:()=>null}};
 const code=ts.transpileModule(readFileSync(new URL('../components/dashboard/ProcessorSettings.tsx',import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX}}).outputText;
 const module={exports:{}};new Function('require','module','exports',code)(name=>imports[name]??require(name),module,module.exports);
 const render=()=>{cursor=0;const tree=module.exports.ProcessorSettings({});mounted=true;return tree;};
 return {render,async mount(){render();for(const effect of effects)effect()?.();await flush();return render();}};
}
function nodes(node){if(!node||typeof node!=='object')return [];if(Array.isArray(node))return node.flatMap(nodes);return [node,...nodes(node.props?.children)];}
function text(node){if(node==null||typeof node==='boolean')return '';if(typeof node!=='object')return String(node);if(Array.isArray(node))return node.map(text).join('');return text(node.props?.children);}
function button(tree,label){return nodes(tree).find(n=>n.type==='button'&&text(n)===label);}
const flush=()=>new Promise(resolve=>setImmediate(resolve));
function advanced(ui){nodes(ui.render()).find(n=>n.type==='details'&&n.props.onToggle).props.onToggle({currentTarget:{open:true}});return ui.render();}
function response(path,health,runs=[]){return Response.json(path.endsWith('/health')?health:path.endsWith('/cluster')?{workers:[]}:runs);}

test('checkpoint reset is advanced, requires confirmation, preserves pause and reports errors',async()=>{
 const health={enabled:false,counts:{queued:0,running:0}},original=globalThis.fetch,calls=[];let reject=true;
 globalThis.fetch=async(path,options)=>{calls.push({path,options});if(path.endsWith('/reset'))return Response.json(reject?{error:'pause processing first'}:{checkpoints_reset:2},{status:reject?409:200});return response(path,health);};
 try{const ui=harness();let tree=await ui.mount();assert.equal(button(tree,'Reset checkpoints'),undefined);tree=advanced(ui);assert.equal(button(tree,'Reset checkpoints').props.disabled,false);button(tree,'Reset checkpoints').props.onClick();tree=ui.render();assert.match(text(tree),/existing input will be read again/);button(tree,'Confirm checkpoint reset').props.onClick();await flush();tree=ui.render();assert.match(text(tree),/pause processing first/);reject=false;button(tree,'Confirm checkpoint reset').props.onClick();await flush();tree=ui.render();assert.match(text(tree),/Reset 2 checkpoints. Processing remains paused/);assert.equal(button(tree,'Confirm checkpoint reset'),undefined);const writes=calls.filter(c=>c.options?.method);assert.equal(writes.length,2);for(const write of writes){assert.equal(write.path,'/api/processor/checkpoints/reset');assert.deepEqual(JSON.parse(write.options.body),{confirm:'reset-checkpoints'});}}finally{globalThis.fetch=original;}
});

test('empty configuration offers setup and disables starting until configured',async()=>{
 const original=globalThis.fetch;globalThis.fetch=async path=>response(path,{enabled:false,counts:{},blocked_reason:'No programs configured.'});
 try{const ui=harness(),tree=await ui.mount();assert.match(text(tree),/Setup required/);assert.ok(button(tree,'Set up a program'));assert.equal(button(tree,'Start processing').props.disabled,true);}finally{globalThis.fetch=original;}
});

test('three sections load on demand and preserve the editor while viewing results',async()=>{
 const original=globalThis.fetch;globalThis.fetch=async path=>response(path,{enabled:true,counts:{}});
 try{const ui=harness();let tree=await ui.mount();assert.equal(nodes(tree).some(n=>n.type===Editor),false);button(tree,'Programs').props.onClick();tree=ui.render();assert.ok(nodes(tree).some(n=>n.type===Editor));button(tree,'Results').props.onClick();tree=ui.render();assert.ok(nodes(tree).some(n=>n.type===Results));assert.ok(nodes(tree).find(n=>n.props?.id==='processor-programs').props.hidden);assert.ok(nodes(tree).some(n=>n.type===Editor));}finally{globalThis.fetch=original;}
});

test('failed refresh preserves activity and disables controls with unknown service state',async()=>{
 const original=globalThis.fetch;globalThis.fetch=async path=>response(path,{enabled:true,counts:{running:1}},[{ID:'retained-run',ProgramID:'demo',Status:'running'}]);
 try{const ui=harness();let tree=await ui.mount();assert.match(text(tree),/demo/);globalThis.fetch=async()=>{throw new Error('Service unavailable');};button(tree,'Refresh').props.onClick();await flush();tree=ui.render();assert.match(text(tree),/demo/);assert.match(text(tree),/Showing the last available status/);assert.equal(button(tree,'Pause processing').props.disabled,true);}finally{globalThis.fetch=original;}
});

test('cancellation requires confirmation before writing to the API',async()=>{
 const original=globalThis.fetch,calls=[];globalThis.fetch=async(path,options)=>{if(options?.method)calls.push(path);return response(path,{enabled:true,counts:{running:1}},[{ID:'run-one',ProgramID:'demo',Status:'running',PublishedLines:5}]);};
 try{const ui=harness();let tree=await ui.mount();button(tree,'Cancel run').props.onClick();tree=ui.render();assert.equal(calls.length,0);assert.match(text(tree),/Results already saved are kept/);button(tree,'Confirm cancellation').props.onClick();await flush();assert.deepEqual(calls,['/api/processor/runs/run-one/cancel']);}finally{globalThis.fetch=original;}
});

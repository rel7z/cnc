import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import ts from 'typescript';
import React from 'react';
const require=createRequire(import.meta.url);
test('scanner form sends automatic concurrency and timeout for CMS and enum',async()=>{
 const savedFetch=globalThis.fetch;let states=[],cursor=0,effect,calls=[];
 const hooks={useState(initial){const i=cursor++;if(!(i in states))states[i]=initial;return[states[i],v=>{states[i]=typeof v==='function'?v(states[i]):v}]},useEffect(fn){effect??=fn},useCallback(fn){return fn}};
 const options=[{name:'auto_concurrency',flag:'-auto-concurrency',type:'boolean',default:true},{name:'threads',flag:'-t',type:'number',default:256},{name:'timeout',flag:'-timeout',type:'number',default:10}];
 const tools=['cms-scan','enum'].map(id=>({id,name:id,executable:id,scope:'worker',default_mode:'spread',options}));
 globalThis.fetch=async(url,o)=>{if(url==='/api/tools')return Response.json(tools);calls.push(JSON.parse(o.body));return Response.json({job_id:'owned-fixture'})};
 try{
 const source=readFileSync(new URL('../components/dashboard/ToolsLauncher.tsx',import.meta.url),'utf8');const js=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX}}).outputText;
 const module={exports:{}};const select=()=>null;
 const imports={react:hooks,'./ToolSelectDropdown':{ToolSelectDropdown:select},'./VersionTemplateControls':{VersionTemplateControls:()=>null},'./VersionRuleEditor':{VersionRuleEditor:()=>null},'@/lib/version-rules':{normalizeVersionChecks:()=>[]}};
 new Function('require','module','exports',js)(name=>imports[name]??require(name),module,module.exports);
 const render=()=>{cursor=0;return module.exports.ToolsLauncher()};const find=(node,p)=>{if(!node||typeof node!=='object')return;if(p(node))return node;for(const c of React.Children.toArray(node.props?.children)){const found=find(c,p);if(found)return found}};
 render();effect();await new Promise(r=>setImmediate(r));
 let tree=render();assert.equal(find(tree,n=>n.props?.id==='tool-opt-auto_concurrency').props.checked,true);
 find(tree,n=>n.props?.id==='tool-opt-auto_concurrency').props.onChange({target:{checked:false}});find(render(),n=>n.props?.id==='tool-opt-timeout').props.onChange({target:{value:'5'}});
 await find(render(),n=>n.type==='form').props.onSubmit({preventDefault(){}});
 assert.equal(calls[0].options.auto_concurrency,false);assert.equal(calls[0].options.timeout,5);assert.equal(calls[0].output_file,undefined);
 const versionShortcut=find(render(),n=>n.type==='button'&&n.props.children==='Scan PHP or CMS versions');assert.ok(versionShortcut);versionShortcut.props.onClick();await find(render(),n=>n.type==='form').props.onSubmit({preventDefault(){}});
 assert.equal(calls[1].options.auto_concurrency,true);assert.equal(calls[1].options.threads,256);assert.equal(calls[1].options.timeout,10);assert.deepEqual(calls[1].version_rules,[]);assert.equal(calls[1].target_authorization,undefined);
 }finally{globalThis.fetch=savedFetch}
});

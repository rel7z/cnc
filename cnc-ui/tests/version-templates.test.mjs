import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import ts from 'typescript';
import React from 'react';
import * as rules from '../lib/version-rules.ts';
import * as auth from '../lib/auth.ts';
const require = createRequire(import.meta.url);
function compile(path, imports) {
  const source = readFileSync(new URL(path, import.meta.url), 'utf8');
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX } }).outputText;
  const module = { exports: {} };
  new Function('require','module','exports',compiled)(name => imports[name] ?? require(name),module,module.exports);
  return module.exports;
}
const helpers = compile('../lib/version-templates.ts', { './version-rules': rules });
const textHelpers = compile('../lib/version-template-text.ts', {'./version-rules':rules,'./version-templates':helpers});
const checks = [
  { ...rules.newVersionCheck('range'), identifier:'elementor', clauses:[{operator:'>=',version:'3.24'},{operator:'<=',version:'3.25'}] },
  { ...rules.newVersionCheck('custom'), target:'joomla:plugin',identifier:'example',group:'system',name:'Custom',clauses:[{operator:'>=',version:'1'},{operator:'<',version:'2'}] },
];
test('templates round-trip editable checks and refresh IDs without secure-context crypto', () => {
  const template = { id:'fixture', name:'Plugins',checks,revision:1 };
  helpers.validateTemplateDraft(template.name,checks);
  const copy = helpers.loadTemplateChecks(template,checks);
  assert.equal(helpers.templateChecksKey(copy),helpers.templateChecksKey(checks));
  assert.ok(copy.every(c => !checks.some(old => old.id === c.id)));
  assert.equal(rules.comparisonOf(copy[0]),'between');
  assert.equal(rules.comparisonOf(copy[1]),'custom');
  assert.equal(copy[1].group,'system'); assert.equal(copy[0].name,'');
  copy[0].clauses[0].version = '5'; assert.equal(checks[0].clauses[0].version,'3.24');
  for (const [name, draft] of [['',checks],['Empty',[]],['Invalid',[rules.newVersionCheck('invalid')]],['Dup',[checks[0],{...checks[0],id:'different'}]]]) assert.throws(() => helpers.validateTemplateDraft(name,draft));
});

test('template UI CRUD, confirmation, dirty edits and failed saves preserve form state', async () => {
  let states = [], cursor = 0, pendingEffect;
  const hooks = {
    useState(initial) { const i = cursor++; if (!(i in states)) states[i] = initial; return [states[i], value => { states[i] = typeof value === 'function' ? value(states[i]) : value; }]; },
    useRef() { return {current:null}; },
    useEffect(effect) { if (!pendingEffect) pendingEffect = effect; },
  };
  let current = structuredClone(checks), revision = 1, stored = null, fail = false;
  const calls=[];
  const fakeRequest = async (path='',method='GET',body) => {
    calls.push({path,method,body});
    if (fail) throw new Error('storage unavailable');
    if (method==='GET') return path ? stored : stored ? [stored] : [];
    if (method==='DELETE') { stored=null; return {deleted:true}; }
    stored={id:'fixture',name:body.name,checks:structuredClone(body.checks),revision:revision++,created_at:'now',updated_at:'now'};
    return stored;
  };
  const { VersionTemplateControls } = compile('../components/dashboard/VersionTemplateControls.tsx', {react:hooks,'@/lib/version-templates':{...helpers,templateRequest:fakeRequest},'@/lib/version-rules':rules,'@/lib/version-template-text':textHelpers});
  const render = () => { cursor=0; return VersionTemplateControls({rules:current,onRules:next => { current=next; }}); };
  function find(element,predicate) {
    if (!element || typeof element !== 'object') return;
    if (predicate(element)) return element;
    for (const child of React.Children.toArray(element.props?.children)) { const result=find(child,predicate); if(result) return result; }
  }
  const label = name => find(render(), e => e.props?.['aria-label']===name);
  const button = name => find(render(), e => e.type==='button' && e.props.children===name);
  const flush = async () => { await new Promise(resolve => setImmediate(resolve)); };
  render(); pendingEffect(); await flush();
  label('Template name').props.onChange({target:{value:'Plugins'}});
  button('Save as new').props.onClick(); await flush();
  assert.equal(stored.name,'Plugins'); assert.equal(button('Update').props.disabled,false);
  current=[{...current[0],identifier:'other'},current[1]];
  button('Load').props.onClick(); assert.ok(label('Replace current version checks'));
  button('Cancel').props.onClick(); assert.equal(current[0].identifier,'other');
  button('Load').props.onClick(); button('Replace checks').props.onClick(); await flush();
  assert.equal(current[0].identifier,'elementor'); assert.notEqual(current[0].id,checks[0].id);
  label('Template name').props.onChange({target:{value:'Renamed'}});
  fail=true; button('Update').props.onClick(); await flush();
  assert.equal(label('Template name').props.value,'Renamed'); assert.equal(stored.name,'Plugins');
  fail=false; button('Update').props.onClick(); await flush();
  assert.equal(stored.name,'Renamed'); assert.equal(calls.at(-1).body.revision,1);
  button('Delete').props.onClick(); assert.ok(label('Delete template')); assert.ok(stored);
  button('Confirm delete').props.onClick(); await flush();
  assert.equal(stored,null); assert.equal(current.length,2);
  assert.equal(calls.at(-1).body.revision,2);
});

test('dashboard template proxy enforces session, origin, size, and server-only credentials', async () => {
  const previous={fetch:globalThis.fetch,token:process.env.CNC_PROCESSOR_TOKEN,secret:process.env.CNC_SESSION_SECRET};
  process.env.CNC_SESSION_SECRET='fixture-session-secret-at-least-32-characters';
  process.env.CNC_PROCESSOR_TOKEN='fixture-processor-secret-at-least-32-characters';
  const { proxyVersionTemplates } = compile('../lib/version-template-proxy.ts',{'@/lib/auth':auth});
  const request=(method,body,session=true,origin='http://dashboard.test') => {
    const r=new Request('http://dashboard.test/api/version-check-templates',{method,headers:{host:'dashboard.test',origin},...(method==='GET'?{}:{body})});
    r.cookies={get:()=>session?{value:auth.createSession()}:undefined};return r;
  };
  let upstream;
  globalThis.fetch=async (url,options) => { upstream={url,options};return Response.json({id:'fixture'}); };
  try {
    assert.equal((await proxyVersionTemplates(request('GET',null,false))).status,401);
    assert.equal((await proxyVersionTemplates(request('POST','{}',true,'http://elsewhere.test'))).status,403);
    assert.equal((await proxyVersionTemplates(request('POST','x'.repeat(8*1024*1024+1)))).status,413);
    const response=await proxyVersionTemplates(request('POST','{}'));
    assert.equal(response.status,200); assert.equal(upstream.url,'http://127.0.0.1:9191/v1/version-check-templates');
    assert.equal(upstream.options.headers.Authorization,'Bearer '+process.env.CNC_PROCESSOR_TOKEN);
    assert.doesNotMatch(await response.text(),/secret/);
    assert.equal((await proxyVersionTemplates(request('GET'), '../unsafe')).status,400);
    globalThis.fetch=async()=>{throw new Error('offline');};
    assert.equal((await proxyVersionTemplates(request('GET'))).status,503);
  } finally {
    globalThis.fetch=previous.fetch;
    for(const [key,value] of [['CNC_PROCESSOR_TOKEN',previous.token],['CNC_SESSION_SECRET',previous.secret]]) if(value===undefined) delete process.env[key];else process.env[key]=value;
  }
});


test('API forwarding runs only after dashboard dynamic handlers are matched', async () => {
  const { default: config } = compile('../next.config.ts', {});
  const rewrites = await config.rewrites();
  assert.ok(Array.isArray(rewrites.fallback));
  assert.equal(rewrites.afterFiles, undefined);
  assert.equal(rewrites.fallback[0].source, '/api/:path*');
});

test('TXT operators, all CMS identities, inclusive/Advanced conditions, names and fresh IDs', () => {
  const guide=readFileSync(new URL('../public/version-template-format.txt',import.meta.url),'utf8');
  const parsed=textHelpers.parseTemplateText(guide,'guide.txt',checks);
  assert.equal(parsed.name,'All TXT template formats');
  assert.equal(parsed.checks.length,18);
  assert.deepEqual(new Set(parsed.checks.slice(0,5).map(c=>c.clauses[0].operator)),new Set(['=','<','<=','>','>=']));
  assert.equal(rules.comparisonOf(parsed.checks[5]),'between');
  assert.equal(rules.comparisonOf(parsed.checks[6]),'custom');
  assert.deepEqual(new Set(parsed.checks.map(c=>c.target)),new Set(rules.componentTypes));
  assert.equal(parsed.checks.find(c=>c.target==='joomla:plugin').group,'system');
  const text=textHelpers.serializeTemplateText(parsed.name,parsed.checks);
  const again=textHelpers.parseTemplateText('\uFEFF'+text.replaceAll('\n','\r\n'),'again.txt',parsed.checks);
  assert.equal(helpers.templateChecksKey(again.checks),helpers.templateChecksKey(parsed.checks));
  assert.ok(again.checks.every(c=>!parsed.checks.some(old=>old.id===c.id)));
  assert.throws(()=>textHelpers.parseTemplateText('elementor<3.25.0','no-space.txt'), /Line 1/);
});

test('corrected default TXT and seeded database template contain the same seven requested checks', () => {
  const source=readFileSync(new URL('../public/default-version-checks.txt',import.meta.url),'utf8');
  const parsed=textHelpers.parseTemplateText(source,'default-version-checks.txt');
  const seeded=JSON.parse(readFileSync(new URL('../../processor/internal/store/default-version-checks.json',import.meta.url),'utf8'));
  assert.equal(parsed.checks.length,7);
  assert.deepEqual(parsed.checks.map(({id,...check})=>check),seeded.map(({id,...check})=>check));
  assert.deepEqual(parsed.checks[2].clauses,[{operator:'<=',version:'3.3.1'}]);
  assert.equal(parsed.checks[2].identifier,'translatepress-multilingual');
  assert.deepEqual(parsed.checks.filter(c=>c.target==='php:php-src').map(c=>c.clauses),[['8.5.0','8.5.12'],['8.4.0','8.4.27'],['8.3.0','8.3.36'],['8.2.0','8.2.35']].map(([min,max])=>[{operator:'>=',version:min},{operator:'<',version:max}]));
  helpers.validateTemplateDraft(parsed.name,parsed.checks);
  assert.equal(textHelpers.parseTemplateText(textHelpers.serializeTemplateText(parsed.name,parsed.checks)).checks.length,7);
});

test('TXT and database validation have no fixed check-count limit', () => {
  const source=Array.from({length:1001},(_,i)=>`owned-${i} < 3.1.8.10`).join('\n');
  const parsed=textHelpers.parseTemplateText(source,'many.txt');
  assert.equal(parsed.checks.length,1001);
  helpers.validateTemplateDraft(parsed.name,parsed.checks);
});

test('version form loads the seeded default once and preserves early user edits', async () => {
  const seeded=JSON.parse(readFileSync(new URL('../../processor/internal/store/default-version-checks.json',import.meta.url),'utf8'));
  const saved={id:'builtin-cms-default-20261014',name:'Default CMS version checks',checks:seeded,revision:1,is_default:true};
  async function mount(earlyEdits) {
    let states=[],refs=[],cursor=0,effect,current=[];
    const hooks={useState(initial){const i=cursor++;if(!(i in states))states[i]=initial;return[states[i],v=>{states[i]=typeof v==='function'?v(states[i]):v;}];},useRef(initial){const i=cursor++;return refs[i]??(refs[i]={current:initial});},useEffect(callback){effect=callback;}};
    const {VersionTemplateControls}=compile('../components/dashboard/VersionTemplateControls.tsx',{react:hooks,'@/lib/version-templates':{...helpers,templateRequest:async()=>[saved]},'@/lib/version-rules':rules,'@/lib/version-template-text':textHelpers});
    const render=()=>{cursor=0;return VersionTemplateControls({rules:current,onRules:next=>{current=next;}});};
    render();
    if(earlyEdits){current=[checks[0]];render();}
    effect();await new Promise(resolve=>setImmediate(resolve));
    return {current,tree:render()};
  }
  const loaded=await mount(false);
  assert.equal(loaded.current.length,7);
  function find(e,p){if(!e||typeof e!=='object')return;if(p(e))return e;for(const c of React.Children.toArray(e.props?.children)){const result=find(c,p);if(result)return result;}}
  assert.equal(find(loaded.tree,e=>e.props?.['aria-label']==='Templates').props.value,'builtin-cms-default-20261014');
  assert.equal((await mount(true)).current.length,1);
});

test('TXT rejects invalid lines, names, duplicates, ranges, operators and oversize files', async () => {
  for(const line of ['Elementor < 3.25.0','elementor != 3.25.0','elementor == 3.25.0','elementor < 3.*','elementor >= 4 & <= 3','joomla:plugin:example < 2','joomla:component:com_example < 2','elementor < 3 |','elementor < 3 &','elementor < 3 | "broken','wordpress:core:extra < 3']) {
    assert.throws(()=>textHelpers.parseTemplateText('# comment\n'+line),/Line 2/);
  }
  assert.throws(()=>textHelpers.parseTemplateText('elementor < 3\nelementor < 3.0.0'),/Line 2/);
  assert.throws(()=>textHelpers.parseTemplateText('# template: A\n# template: B\nelementor < 3'),/Line 2/);
  assert.throws(()=>textHelpers.parseTemplateText('# empty'),/valid version check/);
  assert.throws(()=>textHelpers.parseTemplateText('x'.repeat(textHelpers.TEMPLATE_TEXT_LIMIT+1)),/4 MiB/);
  const label='Quoted "name"\nsecond line';
  const draft=[{...checks[0],name:label}];
  const text=textHelpers.serializeTemplateText(label,draft);
  const parsed=textHelpers.parseTemplateText(text);
  assert.equal(parsed.name,label);assert.equal(parsed.checks[0].name,label);
  const bytes=new TextEncoder().encode('elementor < 3.25.0');
  const file={name:'My plugin checks.TXT',size:bytes.length,arrayBuffer:async()=>bytes.buffer};
  assert.equal((await textHelpers.readTemplateTextFile(file,[])).name,'My plugin checks');
  await assert.rejects(textHelpers.readTemplateTextFile({...file,name:'bad.json'},[]),/\.txt/);
  await assert.rejects(textHelpers.readTemplateTextFile({...file,size:textHelpers.TEMPLATE_TEXT_LIMIT+1},[]),/4 MiB/);
  await assert.rejects(textHelpers.readTemplateTextFile({...file,arrayBuffer:async()=>new Uint8Array([0xff]).buffer},[]),/UTF-8/);
  assert.equal(textHelpers.templateTextFilename('../My checks!'),'My-checks.txt');
});

test('TXT upload loads into the form, confirms unsaved replacement, and never saves until requested', async () => {
  let states=[],cursor=0;
  const hooks={useState(initial){const i=cursor++;if(!(i in states))states[i]=initial;return[states[i],v=>{states[i]=typeof v==='function'?v(states[i]):v;}];},useRef(){return{current:null};},useEffect(){}};
  let current=structuredClone(checks),stored=null;
  const calls=[];
  const fakeRequest=async(path='',method='GET',body)=>{calls.push({path,method,body});stored={id:'saved',name:body.name,checks:body.checks,revision:1};return stored;};
  const {VersionTemplateControls}=compile('../components/dashboard/VersionTemplateControls.tsx',{react:hooks,'@/lib/version-templates':{...helpers,templateRequest:fakeRequest},'@/lib/version-rules':rules,'@/lib/version-template-text':textHelpers});
  const render=()=>{cursor=0;return VersionTemplateControls({rules:current,onRules:next=>{current=next;}});};
  function find(e,p){if(!e||typeof e!=='object')return;if(p(e))return e;for(const c of React.Children.toArray(e.props?.children)){const r=find(c,p);if(r)return r;}}
  const button=name=>find(render(),e=>e.type==='button'&&e.props.children===name);
  const label=name=>find(render(),e=>e.props?.['aria-label']===name);
  const bytes=new TextEncoder().encode('# template: Uploaded combo\nelementor < 3.25.0\n');
  const file={name:'combo.txt',size:bytes.length,arrayBuffer:async()=>bytes.buffer};
  const upload=async f=>{label('Upload TXT template file').props.onChange({target:{files:[f],value:'file'}});await new Promise(r=>setImmediate(r));};
  await upload(file);assert.ok(label('Replace current version checks'));assert.equal(current.length,2);
  button('Cancel').props.onClick();assert.equal(current.length,2);assert.equal(calls.length,0);
  await upload(file);button('Replace checks').props.onClick();
  assert.equal(current.length,1);assert.equal(current[0].identifier,'elementor');assert.equal(current[0].clauses[0].operator,'<');
  assert.equal(label('Template name').props.value,'Uploaded combo');assert.equal(calls.length,0);assert.equal(button('Update').props.disabled,true);
  await upload({...file,arrayBuffer:async()=>new TextEncoder().encode('invalid').buffer});
  assert.equal(current.length,1);assert.equal(label('Template name').props.value,'Uploaded combo');
  button('Save as new').props.onClick();await new Promise(r=>setImmediate(r));
  assert.equal(calls.length,1);assert.equal(calls[0].method,'POST');assert.equal(stored.name,'Uploaded combo');
  // A second uploaded file detaches from the just-saved database template.
  await upload(file);assert.equal(button('Update').props.disabled,true);assert.equal(label('Templates').props.value,'');
  label('Templates').props.onChange({target:{value:'saved'}});
  button('Update').props.onClick();assert.ok(label('Replace saved template'));assert.equal(calls.length,1);
  button('Replace saved template').props.onClick();await new Promise(r=>setImmediate(r));
  assert.equal(calls[1].method,'PUT');assert.equal(calls[1].body.revision,1);
});

"use client";

import {useEffect,useRef,useState} from "react";
import {blankDefinition,editorRequest,outputObject,parseProcessorTemplates,serializeProcessorTemplates,serializeControllerConfig,validateDefinition,type Definition,type Output,type Program,type Runtime,type SavedTemplate,type SetupState} from "@/lib/processor-editor";
const inputClass="mt-1 w-full rounded-md border border-gray-700 bg-gray-950 px-3 py-1.5 text-sm text-white disabled:opacity-50";
const buttonClass="rounded-md border border-gray-700 px-3 py-1.5 text-xs font-medium text-gray-200 hover:bg-gray-800 disabled:opacity-40";
const splitFiles=(text:string)=>text.split('\n').map(s=>s.trim()).filter(Boolean);
function download(name:string,text:string,type="application/json"){
 const url=URL.createObjectURL(new Blob([text],{type}));const a=document.createElement('a');a.href=url;a.download=name;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
}
export function ProcessorEditor(){
 const [draft,setDraft]=useState<Definition>(blankDefinition);
 const [collection,setCollection]=useState<Definition[]>([]);const [collectionIndex,setCollectionIndex]=useState(0);
 const [appliedTools,setAppliedTools]=useState<string[]>([]);
 const [state,setState]=useState<SetupState|null>(null);
 const [templates,setTemplates]=useState<SavedTemplate[]>([]);
 const [templateID,setTemplateID]=useState('');const [templateName,setTemplateName]=useState('');
 const [baseline,setBaseline]=useState(JSON.stringify(blankDefinition()));
 const [busy,setBusy]=useState(false);const [error,setError]=useState('');const [message,setMessage]=useState('');
 const [showErrors,setShowErrors]=useState(false);const [files,setFiles]=useState<File[]>([]);
 const [editing,setEditing]=useState(false);
 const [source,setSource]=useState<'upload'|'server'>('upload');
 const templateInput=useRef<HTMLInputElement>(null);const fileDrafts=useRef<Record<string,File[]>>({});
 const errors=validateDefinition(draft);const dirty=JSON.stringify(draft)!==baseline;
 const selected=templates.find(t=>t.id===templateID);
 const keys=Object.keys(draft.manifest.scripts);
 async function refresh(){const [setups,saved]=await Promise.all([editorRequest('setups'),editorRequest('templates')]);setState(setups);setTemplates(saved);return setups as SetupState;}
 useEffect(()=>{void refresh().catch(e=>setError(e.message));},[]);
 function change(next:Definition){setDraft(next);if(collection.length)setCollection(items=>items.map((item,i)=>i===collectionIndex?next:item));setAppliedTools([]);setMessage('');}
 function program(key:string,patch:Partial<Program>){change({...draft,manifest:{...draft.manifest,scripts:{...draft.manifest.scripts,[key]:{...draft.manifest.scripts[key],...patch}}}});}
 function renameProgram(old:string,key:string){
  if(key===old)return;if(Object.hasOwn(draft.manifest.scripts,key)){setError('That program key already exists.');return;}
  const scripts:Record<string,Program>={};for(const [id,s] of Object.entries(draft.manifest.scripts))scripts[id===old?key:id]={...s,outputs:s.outputs.map(o=>{const value=outputObject(o);return value.send_to===old?{...value,send_to:key}:value;})};
  change({...draft,manifest:{...draft.manifest,scripts}});
 }
 function addProgram(){let n=keys.length+1;while(keys.includes('program'+n))n++;program('program'+n,{path:'',runtime:'',input:draft.manifest.input||'input.txt',outputs:[{path:'success.txt',drive:true}]});}
 function removeProgram(key:string){
  const scripts:Record<string,Program>={};for(const [id,s] of Object.entries(draft.manifest.scripts))if(id!==key)scripts[id]={...s,outputs:s.outputs.map(o=>{const v=outputObject(o);return v.send_to===key?{...v,send_to:''}:v;})};change({...draft,manifest:{...draft.manifest,scripts}});
 }
 function output(key:string,index:number,patch:Partial<Output>){program(key,{outputs:draft.manifest.scripts[key].outputs.map((o,i)=>i===index?{...outputObject(o),...patch}:o)});}
 function load(definition:Definition,name=''){
  if((dirty||collection.length>1)&&!confirm('Replace the current draft? Active server setups will stay unchanged. Download the combined template first if you want to keep these drafts.'))return false;
  setEditing(true);const copy=structuredClone(definition);setCollection([]);fileDrafts.current={};setAppliedTools([]);setDraft(copy);setBaseline(JSON.stringify(copy));setFiles([]);setSource(copy.folder?'server':'upload');setTemplateName(name);setShowErrors(false);setError('');setMessage('Review your setup below, then save your changes.');return true;
 }
 async function action(work:()=>Promise<void>){setBusy(true);setError('');setMessage('');try{await work();}catch(e){setError(e instanceof Error?e.message:'Request failed. Your edits are still here.');}finally{setBusy(false);}}
 function check(){setShowErrors(true);if(Object.keys(errors).length)throw new Error('Please correct the highlighted fields.');}
 async function saveTemplate(update:boolean){await action(async()=>{check();if(!templateName.trim())throw new Error('Enter a template name.');if(update&&!selected)throw new Error('Choose a saved template first.');const saved=await editorRequest('templates'+(update?'/'+selected!.id:''),update?'PUT':'POST',{name:templateName,definition:draft,revision:update?selected!.revision:undefined});setTemplateID(saved.id);setBaseline(JSON.stringify(draft));await refresh();setMessage(update?'Template updated. Active programs have not changed.':'Template saved.');});}
 async function apply(){await action(async()=>{
  check();if(!state)throw new Error('Refresh saved programs before applying.');let definition=structuredClone(draft);
  if(source==='upload'&&files.length){if(files.length>256||files.reduce((sum,file)=>sum+file.size,0)>64*1024*1024)throw new Error('Choose at most 256 files totaling 64 MiB.');const body=new FormData();for(const file of files){const relative=file.webkitRelativePath?file.webkitRelativePath.split('/').slice(1).join('/'):file.name;body.append('file:'+relative,file,file.name);}const uploaded=await editorRequest('assets','POST',body);definition.folder=uploaded.folder;setDraft(definition);setCollection(items=>items.map((item,i)=>i===collectionIndex?definition:item));fileDrafts.current[definition.tool_id]=[];setFiles([]);}
  if(!definition.folder)throw new Error('Choose program files or enter their existing server folder.');
  const next=await editorRequest('setups','PUT',{definition,revision:state.revision});setState(next);setDraft(definition);setBaseline(JSON.stringify(definition));if(collection.length)setAppliedTools(ids=>[...new Set([...ids,definition.tool_id])]);setMessage(`Saved ${definition.tool_id}. Matching input files will use this setup when processing is enabled.`);setEditing(false);
 });}
 async function uploadTemplate(file?:File){if(!file)return;await action(async()=>{const parsed=parseProcessorTemplates(await file.text(),draft);if(load(parsed.definitions[0],parsed.name)){setTemplateID('');setCollection(parsed.definitions.length>1?parsed.definitions:[]);setCollectionIndex(0);setMessage(parsed.definitions.length>1?'Combined template loaded. All tools are retained; select one to review. Active server setups are unchanged until applied.':'Uploaded template loaded into the form. Active server setups are unchanged until applied.');}});}
 function selectCollection(index:number){fileDrafts.current[draft.tool_id]=files;const d=collection[index];setCollectionIndex(index);setDraft(structuredClone(d));setBaseline(JSON.stringify(d));setSource(fileDrafts.current[d.tool_id]?.length||!d.folder?'upload':'server');setFiles(fileDrafts.current[d.tool_id]||[]);setShowErrors(false);setError('');}
 async function applyCollection(){await action(async()=>{
  if(!state)throw new Error('Refresh saved programs before applying.');
  if(files.length||Object.values(fileDrafts.current).some(items=>items.length))throw new Error('Apply each tool with selected program files first to upload them, then use Apply all. Your selected files are retained when switching tools.');
  const definitions=parseProcessorTemplates(serializeProcessorTemplates(templateName,collection),draft).definitions;
  if(definitions.some(d=>!d.folder))throw new Error('Every tool needs its server folder before applying the combined template.');
  let current=state;const completed=[...appliedTools];
  for(const definition of definitions){
   if(completed.includes(definition.tool_id))continue;
   try{current=await editorRequest('setups','PUT',{definition,revision:current.revision});setState(current);completed.push(definition.tool_id);setAppliedTools([...completed]);}
   catch(e){throw new Error(`Could not apply ${definition.tool_id}: ${e instanceof Error?e.message:'Request failed'}. Applied tools: ${completed.join(', ')||'none'}. All template edits are retained; fix the error and retry remaining tools.`);}
  }
  setMessage('All tools applied. Existing unrelated setups were preserved.');
 });}
 const feedback=(key:string)=>showErrors&&errors[key]?<p className="mt-1 text-xs text-red-300" role="alert">{errors[key]}</p>:null;
 return <div className="rounded-xl border border-gray-800 bg-gray-900 p-3 md:p-4 space-y-4">
  <div><h3 className="font-medium text-white">Your programs</h3><p className="mt-1 text-sm text-gray-400">Each setup connects an input file to a program and the results you want to save.</p></div>
  {error&&<p role="alert" className="rounded border border-red-900 bg-red-950/30 p-3 text-sm text-red-200">{error}</p>}
  {message&&<p role="status" className="rounded border border-emerald-900 bg-emerald-950/30 p-3 text-sm text-emerald-200">{message}</p>}
  <fieldset disabled={busy} className="space-y-4 disabled:opacity-60">
   <div hidden={editing}>
   <div className="flex flex-wrap items-center justify-between gap-3">
    <p className="text-xs text-gray-400">{state ? `${state.definitions.length} saved setups` : 'Loading programs…'}</p>
    <div className="flex gap-2"><button className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-500" type="button" onClick={()=>{if(load(blankDefinition()))setTemplateID('');}}>Add program</button><button className={buttonClass} type="button" onClick={()=>void action(async()=>{await refresh();setMessage('Programs refreshed. Your draft is preserved.');})}>Refresh programs</button></div>
   </div>
   {state && !state.definitions.length && !state.external.length && !editing && <div className="rounded-xl border border-dashed border-gray-700 px-5 py-8 text-center"><p className="text-sm text-gray-200">No programs yet</p><p className="mt-2 text-sm text-gray-400">Add your first program or choose a saved template below.</p></div>}
   {!!state?.definitions.length&&<div className="grid gap-3 md:grid-cols-2">{state.definitions.map(d=><div key={d.tool_id} className="rounded-lg border border-gray-700 bg-gray-950 p-4"><div className="flex items-center justify-between gap-3"><strong className="text-sm text-white">{d.tool_id}</strong><button type="button" className={buttonClass} onClick={()=>load(d)}>Edit</button></div><p className="mt-2 text-xs text-gray-500">Incoming: <span className="text-gray-300">{d.incoming}</span></p><div className="mt-3 space-y-1 border-t border-gray-800 pt-2" aria-label="Saved output files">{Object.entries(d.manifest.scripts).flatMap(([key,script])=>script.outputs.map((raw,i)=>{const o=outputObject(raw);return <p key={key+':'+i} className="break-all text-xs text-gray-400"><span className="text-gray-200">{o.path}</span> → <span className="text-blue-300">{o.merged_name || (o.drive === false ? 'Next program' : 'Results & Drive')}</span>{o.drive===false&&<span className="ml-2 text-amber-400">internal only</span>}</p>}))}</div></div>)}</div>}
   {!!state?.external.length&&<p className="text-xs text-gray-500">Existing registrations managed outside this form: {state.external.join(', ')}. Use a new tool ID to add a setup.</p>}
   </div>
   <div hidden={!editing} className="space-y-5 rounded-xl border border-blue-500/20 bg-gray-950/30 p-4 md:p-5">
   <div className="flex items-center justify-between gap-3"><div><h4 className="text-base font-medium text-white">{draft.tool_id || 'New program'}</h4><p className="mt-1 text-xs text-gray-400">Choose input, program files, and output. Then save your setup.</p></div><button type="button" className={buttonClass} onClick={()=>{if(!dirty||confirm('Close this draft? Your edits will stay here until you load another setup.'))setEditing(false);}}>Close editor</button></div>
   {collection.length>1&&<div className="rounded border border-blue-800 p-3 space-y-3"><h4 className="text-sm font-medium text-white">Tools in this template ({collection.length})</h4><label className="text-xs text-gray-400">Review tool<select aria-label="Combined template tool" className={inputClass} value={collectionIndex} onChange={e=>selectCollection(Number(e.target.value))}>{collection.map((d,i)=><option key={i} value={i}>{d.tool_id} — {d.incoming}{appliedTools.includes(d.tool_id)?' (applied)':''}</option>)}</select></label><p className="text-xs text-gray-400">Switching tools keeps edits. Download template exports all tools. Apply all processes tools independently and preserves unrelated active setups. If one fails, earlier successful tools remain applied.</p><p className="text-xs text-gray-400">Combined templates are saved as downloadable JSON files. Database Save/Update controls are for single-tool templates.</p><button type="button" className={buttonClass} disabled={!state} onClick={()=>void applyCollection()}>Apply all tool setups</button></div>}
   <div className="border-t border-gray-800 pt-4"><h4 className="text-sm font-medium text-white">1. Choose the input <span className="font-normal text-gray-500">— {draft.tool_id||'new setup'}</span></h4><p className="mt-1 text-xs text-gray-500">Which incoming file should this program process?</p></div>
   <div className="grid gap-4 md:grid-cols-3">
    <label className="text-sm text-gray-300">Setup name<input aria-label="Processor tool ID" className={inputClass} value={draft.tool_id} onChange={e=>change({...draft,tool_id:e.target.value})} placeholder="my-tool" disabled={collection.length>1}/>{feedback('tool_id')}</label>
    <label className="text-sm text-gray-300">Watch for this file<input aria-label="Incoming CNC filename" className={inputClass} value={draft.incoming} onChange={e=>change({...draft,incoming:e.target.value})} placeholder="wordpress.txt"/>{feedback('incoming')}</label>
    <label className="text-sm text-gray-300">Program reads input from<input aria-label="Default program input filename" className={inputClass} value={draft.manifest.input||''} onChange={e=>change({...draft,manifest:{...draft.manifest,input:e.target.value}})} placeholder="input.txt"/>{feedback('input')}</label>
   </div>
   <div className="space-y-2 rounded-lg border border-gray-800 bg-gray-950/40 p-3"><div><h4 className="text-sm font-medium text-white">2. Program files</h4><p className="mt-1 text-xs text-gray-500">Upload files now or use an existing processor folder.</p></div><div className="flex flex-wrap gap-4 text-sm text-gray-300"><label><input type="radio" name="processor-source" checked={source==='upload'} onChange={()=>setSource('upload')}/> Upload files</label><label><input type="radio" name="processor-source" checked={source==='server'} onChange={()=>setSource('server')}/> Use server folder</label></div>
    {source==='upload'?<div><input aria-label="Program files" type="file" multiple onChange={e=>{setFiles(Array.from(e.target.files||[]));setMessage('');}} className="text-sm text-gray-300"/><label className="mt-3 block text-xs text-gray-400">Or choose a program folder<input aria-label="Program folder upload" type="file" multiple {...{webkitdirectory:""}} onChange={e=>{setFiles(Array.from(e.target.files||[]));setMessage('');}} className="ml-2 text-sm text-gray-300"/></label><p className="mt-2 text-xs text-gray-500">Choose scripts, a compiled Linux binary, and supporting files. Maximum 64 MiB total. Folder uploads preserve nested paths. Only explicitly declared program and supporting files are bundled.</p>{files.length>0&&<p className="text-xs text-gray-300">Selected: {files.map(f=>f.name).join(', ')}</p>}{draft.folder&&<p className="text-xs text-gray-500">Previously saved files remain available unless you choose replacements.</p>}</div>:<label className="block text-sm text-gray-300">Existing server folder (relative to processor directory)<input aria-label="Processor source folder" className={inputClass} value={draft.folder} onChange={e=>change({...draft,folder:e.target.value})} placeholder="tools/my-tool"/>{feedback('folder')}<span className="mt-1 block text-xs text-gray-500">The program must already exist here. A template does not upload its executable; choose Upload program files if it is only on your computer.</span></label>}
   </div>
   <details><summary className="cursor-pointer text-xs text-gray-400">Shared supporting files</summary><label className="mt-2 block text-sm text-gray-300">One relative path per line<textarea aria-label="Shared supporting files" className={inputClass} rows={2} value={(draft.manifest.files||[]).join('\n')} onChange={e=>change({...draft,manifest:{...draft.manifest,files:splitFiles(e.target.value)}})}/>{feedback('files')}</label></details>
   <div><h4 className="text-sm font-medium text-white">3. Choose the results</h4><p className="mt-1 text-xs text-gray-500">Select the program to run and the text files it produces. Results are collected automatically.</p></div>
   <datalist id="processor-uploaded-files">{files.map((f,i)=><option key={i} value={f.webkitRelativePath?f.webkitRelativePath.split('/').slice(1).join('/'):f.name}/>)}</datalist><div className="space-y-3">{keys.map(key=>{const s=draft.manifest.scripts[key];return <div key={draft.tool_id+':'+key} className="rounded-lg border border-gray-800 bg-gray-950/40 p-3 space-y-3">
    <div className="flex items-center justify-between"><h4 className="text-sm font-semibold text-white">Program: {key||'unnamed'}</h4><button type="button" className="text-xs text-red-300 disabled:opacity-40" disabled={keys.length===1} onClick={()=>removeProgram(key)}>Remove program</button></div>
    <label className="block text-sm text-gray-300">Program file<input aria-label={`${key} program filename`} list="processor-uploaded-files" className={inputClass} value={s.path} onChange={e=>program(key,{path:e.target.value})} placeholder="scanner.py"/>{feedback(key+'.path')}</label>
    <details open={showErrors && !!(errors[key+'.key'] || errors[key+'.runtime'] || errors[key+'.input']) || undefined}><summary className="cursor-pointer text-xs text-gray-400">Program options <span className="text-gray-600">· runtime and input filename</span></summary><div className="mt-3 grid gap-3 md:grid-cols-3">
     <label className="text-xs text-gray-400">Program key<input aria-label={`${key} program key`} className={inputClass} defaultValue={key} onBlur={e=>renameProgram(key,e.target.value)}/>{feedback(key+'.key')}</label>
     <label className="text-xs text-gray-400">Runtime<select aria-label={`${key} runtime`} className={inputClass} value={s.runtime||''} onChange={e=>program(key,{runtime:e.target.value as Runtime})}><option value="">Detect from filename</option><option value="python">Python</option><option value="binary">Compiled Linux binary (Go, Rust, C/C++)</option><option value="node">Node.js</option><option value="bash">Bash</option></select>{feedback(key+'.runtime')}</label>
     <label className="text-xs text-gray-400">Input filename override<input aria-label={`${key} input filename`} className={inputClass} value={s.input||''} placeholder={draft.manifest.input||'input.txt'} onChange={e=>program(key,{input:e.target.value})}/>{feedback(key+'.input')}</label>
    </div></details>
    <div className="space-y-2"><p className="text-sm text-gray-300">Output files</p>{s.outputs.map((raw,i)=>{const o=outputObject(raw);return <div key={i} className="grid items-end gap-3 rounded bg-gray-900 p-3 md:grid-cols-2">
     <label className="text-xs text-gray-400">Output filename<input aria-label={`${key} output ${i+1} filename`} className={inputClass} value={o.path} onChange={e=>output(key,i,{path:e.target.value})}/>{feedback(`${key}.output.${i}`)}</label>
     <label className="py-2 text-sm text-gray-300"><input aria-label={`${key} output ${i+1} upload to Drive`} type="checkbox" checked={o.drive!==false} onChange={e=>output(key,i,{drive:e.target.checked})}/> Save to results &amp; Drive</label>
     <details className="md:col-span-2" open={showErrors && !!errors[`${key}.output.${i}`] || undefined}><summary className="cursor-pointer text-xs text-gray-400">Output options · rename or send to another program</summary><div className="mt-3 grid gap-3 md:grid-cols-2">     <label className="text-xs text-gray-400">Save under a different name<input aria-label={`${key} output ${i+1} merged filename`} className={inputClass} value={o.merged_name||''} placeholder={`${draft.tool_id || 'tool'}--${key}__${o.path.split('/').pop() || 'results.txt'}`} onChange={e=>output(key,i,{merged_name:e.target.value})}/><span className="mt-1 block text-[11px] text-gray-500">Optional. Leave blank to use the automatic filename.</span></label>
     <label className="text-xs text-gray-400">Send completed lines to<select aria-label={`${key} output ${i+1} next program`} className={inputClass} value={o.send_to||''} onChange={e=>output(key,i,{send_to:e.target.value})}><option value="">No next program</option>{keys.filter(k=>k!==key).map(k=><option key={k} value={k}>{k}</option>)}</select></label>
     </div></details>
     <button type="button" className={buttonClass} disabled={s.outputs.length===1} onClick={()=>program(key,{outputs:s.outputs.filter((_,index)=>index!==i)})}>Remove output</button>
    </div>})}{feedback(key+'.outputs')}<button type="button" className={buttonClass} onClick={()=>program(key,{outputs:[...s.outputs,{path:'',drive:true}]})}>Add output</button></div>
    <details><summary className="cursor-pointer text-xs text-gray-400">Dependencies</summary><div className="mt-3 grid gap-3 md:grid-cols-3">
     <label className="text-xs text-gray-400">Supporting files, one per line<textarea aria-label={`${key} supporting files`} rows={2} className={inputClass} value={(s.files||[]).join('\n')} onChange={e=>program(key,{files:splitFiles(e.target.value)})}/>{feedback(key+'.files')}</label>
     <label className="text-xs text-gray-400">Python requirements lock<input aria-label={`${key} requirements lock`} className={inputClass} value={s.requirements_lock||''} onChange={e=>program(key,{requirements_lock:e.target.value})} placeholder="requirements.lock"/></label>
     <label className="text-xs text-gray-400">Node package lock<input aria-label={`${key} package lock`} className={inputClass} value={s.package_lock||''} onChange={e=>program(key,{package_lock:e.target.value})} placeholder="package-lock.json"/></label>
    </div></details>
   </div>})}</div>
   {feedback('programs')}{feedback('connections')}
   <div className="flex flex-wrap items-center gap-2 rounded-lg border border-gray-800 bg-gray-950/60 p-3"><button type="button" className={buttonClass} onClick={addProgram}>Add another step</button><button type="button" disabled={!state} onClick={()=>void apply()} className="rounded-md bg-blue-600 px-4 py-1.5 text-sm font-medium text-white hover:bg-blue-500 disabled:opacity-40">{collection.length>1?'Save selected setup':'Save program'}</button>{dirty&&<span className="text-xs text-amber-300">Draft has unsaved changes</span>}</div>
   <p className="text-xs text-gray-500">When processing is enabled, matching input files are handled automatically. Saved changes apply to new runs.</p>
   </div>
   <details className="rounded border border-gray-800 bg-gray-950/40 p-3">
    <summary className="cursor-pointer text-sm font-medium text-gray-200">Templates <span className="ml-1 text-xs font-normal text-gray-500">save, upload, or download</span></summary>
    <div className="mt-3 space-y-3">
    <div className="grid gap-3 sm:grid-cols-2"><label className="text-xs text-gray-400">Saved template<select aria-label="Processor template" className={inputClass} value={templateID} onChange={e=>setTemplateID(e.target.value)}><option value="">Choose a template…</option>{templates.map(t=><option key={t.id} value={t.id}>{t.name}{t.is_default ? " (default)" : ""}</option>)}</select></label><label className="text-xs text-gray-400">Template name<input aria-label="Processor template name" className={inputClass} value={templateName} onChange={e=>setTemplateName(e.target.value)} placeholder="WordPress scanner and checker"/></label></div>
    <div className="flex flex-wrap gap-2">
     <button type="button" className={buttonClass} disabled={!selected} onClick={()=>selected&&load(selected.definition,selected.name)}>Load template</button>
     <button type="button" className={buttonClass} disabled={collection.length>1} onClick={()=>void saveTemplate(false)}>Save as new template</button>
     <button type="button" className={buttonClass} disabled={!selected||collection.length>1} onClick={()=>void saveTemplate(true)}>Update template</button>
     <button type="button" className={buttonClass} disabled={!selected} onClick={()=>{if(selected&&confirm(`Delete template “${selected.name}”?`))void action(async()=>{await editorRequest('templates/'+selected.id,'DELETE',{revision:selected.revision});setTemplateID('');await refresh();setMessage('Template deleted. Form edits are preserved.');});}}>Delete template</button>
     <button type="button" className={buttonClass} onClick={()=>templateInput.current?.click()}>Upload template</button>
     <button type="button" className={buttonClass} onClick={()=>void action(async()=>{check();download((collection.length?'all-tools':draft.tool_id||'processor')+'.template.json',serializeProcessorTemplates(templateName,collection.length?collection:[draft]));})}>{collection.length>1?'Download all tools template':'Download template'}</button>
     <button type="button" className={buttonClass} onClick={()=>void action(async()=>{check();download('config.json',serializeControllerConfig(draft));})}>Download config</button>
     <input ref={templateInput} type="file" accept=".json,.txt,application/json,text/plain" className="hidden" aria-label="Upload processor template file" onChange={e=>{void uploadTemplate(e.target.files?.[0]);e.target.value='';}}/>
    </div><p className="text-xs text-gray-500">Loading a template fills the editor. Save the program to activate it. Program files are uploaded separately.</p>
    </div>
   </details>
  </fieldset>
  {busy&&<p role="status" className="text-sm text-blue-300">Saving or uploading…</p>}
 </div>;
}

export function ProcessorResults(){
 const [results,setResults]=useState<{name:string;lines:number;drive:boolean}[]>([]);const [error,setError]=useState('');
 async function refresh(){try{setResults(await editorRequest('results'));setError('');}catch(e){setError(e instanceof Error?e.message:'Results unavailable');}}
 useEffect(()=>{void refresh();},[]);
 return <div className="rounded-lg border border-gray-700 p-4 space-y-3"><div className="flex items-center justify-between"><h3 className="text-sm font-medium text-white">Download results</h3><button type="button" className={buttonClass} onClick={()=>void refresh()}>Refresh results</button></div><p className="text-xs text-gray-400">Download accepted .txt lines, including partial results from a running or failed program. Internal files remain excluded from Drive.</p>{error&&<p role="alert" className="text-sm text-amber-300">{error}</p>}{!results.length&&!error&&<p className="text-sm text-gray-500">No processor results yet.</p>}<ul className="max-h-72 overflow-y-auto space-y-2">{results.map(r=><li key={r.name} className="flex items-center justify-between gap-3 border-b border-gray-800 py-2 text-xs"><div className="min-w-0"><p className="break-all text-gray-200">{r.name}</p><p className="text-gray-500">{r.lines.toLocaleString()} lines · {r.drive?'Drive output':'Internal output'}</p></div><a className="shrink-0 rounded border border-gray-700 px-3 py-2 text-blue-300" href={'/api/processor/editor/results?name='+encodeURIComponent(r.name)} download>Download .txt</a></li>)}</ul></div>;
}
